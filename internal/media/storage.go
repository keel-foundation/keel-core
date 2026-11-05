package media

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

var ErrStorage = errors.New("media storage error")

type ObjectInfo struct {
	Size        int64
	ContentType string
	ETag        string
}

type CompletedPart struct {
	PartNumber int32
	ETag       string
}

type Storage interface {
	CreateMultipartUpload(ctx context.Context, key, contentType string) (string, error)
	PresignUploadPart(ctx context.Context, key, uploadID string, partNumber int32, expires time.Duration) (string, error)
	CompleteMultipartUpload(ctx context.Context, key, uploadID string, parts []CompletedPart) error
	AbortMultipartUpload(ctx context.Context, key, uploadID string) error
	HeadObject(ctx context.Context, key string) (ObjectInfo, error)
	GetObject(ctx context.Context, key string) (io.ReadCloser, error)
	PutObject(ctx context.Context, key string, body io.Reader, contentType string) error
	PresignPutObject(ctx context.Context, key, contentType string, expires time.Duration) (string, error)
	PresignGetObject(ctx context.Context, key string, expires time.Duration) (string, error)
	DeleteObject(ctx context.Context, key string) error
}

type S3Storage struct {
	client    *s3.Client
	presigner *s3.PresignClient
	bucket    string
}

func NewS3Storage(ctx context.Context, cfg Config) (*S3Storage, error) {
	options := []func(*awsconfig.LoadOptions) error{
		awsconfig.WithRegion(cfg.Region),
	}
	if cfg.AccessKeyID != "" || cfg.SecretAccessKey != "" {
		if cfg.AccessKeyID == "" || cfg.SecretAccessKey == "" {
			return nil, fmt.Errorf("media S3 credentials require both access key id and secret access key")
		}
		options = append(options, awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(cfg.AccessKeyID, cfg.SecretAccessKey, cfg.SessionToken),
		))
	}
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, options...)
	if err != nil {
		return nil, fmt.Errorf("%w: load aws config: %v", ErrStorage, err)
	}
	client := newS3Client(awsCfg, cfg.Endpoint, cfg.UsePathStyle)
	presignClient := client
	if cfg.PublicEndpoint != "" && cfg.PublicEndpoint != cfg.Endpoint {
		presignClient = newS3Client(awsCfg, cfg.PublicEndpoint, cfg.UsePathStyle)
	}
	return &S3Storage{
		client:    client,
		presigner: s3.NewPresignClient(presignClient),
		bucket:    cfg.Bucket,
	}, nil
}

// newS3Client builds an S3 client for one endpoint. Operations use the internal
// endpoint while presigned URLs use the public endpoint, so signature Host
// headers match what the browser or worker connects to.
func newS3Client(awsCfg aws.Config, endpoint string, usePathStyle bool) *s3.Client {
	return s3.NewFromConfig(awsCfg, func(options *s3.Options) {
		options.UsePathStyle = usePathStyle
		if endpoint != "" {
			options.BaseEndpoint = aws.String(endpoint)
		}
	})
}

func (s *S3Storage) CreateMultipartUpload(ctx context.Context, key, contentType string) (string, error) {
	output, err := s.client.CreateMultipartUpload(ctx, &s3.CreateMultipartUploadInput{
		Bucket:      aws.String(s.bucket),
		Key:         aws.String(key),
		ContentType: aws.String(contentType),
	})
	if err != nil {
		return "", fmt.Errorf("%w: create multipart upload: %v", ErrStorage, err)
	}
	if output.UploadId == nil || *output.UploadId == "" {
		return "", fmt.Errorf("%w: create multipart upload returned no id", ErrStorage)
	}
	return *output.UploadId, nil
}

func (s *S3Storage) PresignUploadPart(ctx context.Context, key, uploadID string, partNumber int32, expires time.Duration) (string, error) {
	output, err := s.presigner.PresignUploadPart(ctx, &s3.UploadPartInput{
		Bucket:     aws.String(s.bucket),
		Key:        aws.String(key),
		UploadId:   aws.String(uploadID),
		PartNumber: aws.Int32(partNumber),
	}, s3.WithPresignExpires(expires))
	if err != nil {
		return "", fmt.Errorf("%w: presign upload part: %v", ErrStorage, err)
	}
	return output.URL, nil
}

func (s *S3Storage) CompleteMultipartUpload(ctx context.Context, key, uploadID string, parts []CompletedPart) error {
	sorted := append([]CompletedPart(nil), parts...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].PartNumber < sorted[j].PartNumber })
	completed := make([]types.CompletedPart, 0, len(sorted))
	for _, part := range sorted {
		completed = append(completed, types.CompletedPart{
			ETag:       aws.String(part.ETag),
			PartNumber: aws.Int32(part.PartNumber),
		})
	}
	_, err := s.client.CompleteMultipartUpload(ctx, &s3.CompleteMultipartUploadInput{
		Bucket:          aws.String(s.bucket),
		Key:             aws.String(key),
		UploadId:        aws.String(uploadID),
		MultipartUpload: &types.CompletedMultipartUpload{Parts: completed},
	})
	if err != nil {
		return fmt.Errorf("%w: complete multipart upload: %v", ErrStorage, err)
	}
	return nil
}

func (s *S3Storage) AbortMultipartUpload(ctx context.Context, key, uploadID string) error {
	_, err := s.client.AbortMultipartUpload(ctx, &s3.AbortMultipartUploadInput{
		Bucket:   aws.String(s.bucket),
		Key:      aws.String(key),
		UploadId: aws.String(uploadID),
	})
	if err != nil {
		return fmt.Errorf("%w: abort multipart upload: %v", ErrStorage, err)
	}
	return nil
}

func (s *S3Storage) HeadObject(ctx context.Context, key string) (ObjectInfo, error) {
	output, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return ObjectInfo{}, fmt.Errorf("%w: head object: %v", ErrStorage, err)
	}
	info := ObjectInfo{Size: aws.ToInt64(output.ContentLength)}
	if output.ContentType != nil {
		info.ContentType = *output.ContentType
	}
	if output.ETag != nil {
		info.ETag = *output.ETag
	}
	return info, nil
}

func (s *S3Storage) GetObject(ctx context.Context, key string) (io.ReadCloser, error) {
	output, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, fmt.Errorf("%w: get object: %v", ErrStorage, err)
	}
	return output.Body, nil
}

func (s *S3Storage) PutObject(ctx context.Context, key string, body io.Reader, contentType string) error {
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(s.bucket),
		Key:         aws.String(key),
		Body:        body,
		ContentType: aws.String(contentType),
	})
	if err != nil {
		return fmt.Errorf("%w: put object: %v", ErrStorage, err)
	}
	return nil
}

func (s *S3Storage) PresignPutObject(ctx context.Context, key, contentType string, expires time.Duration) (string, error) {
	output, err := s.presigner.PresignPutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(s.bucket),
		Key:         aws.String(key),
		ContentType: aws.String(contentType),
	}, s3.WithPresignExpires(expires))
	if err != nil {
		return "", fmt.Errorf("%w: presign put object: %v", ErrStorage, err)
	}
	return output.URL, nil
}

func (s *S3Storage) PresignGetObject(ctx context.Context, key string, expires time.Duration) (string, error) {
	output, err := s.presigner.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	}, s3.WithPresignExpires(expires))
	if err != nil {
		return "", fmt.Errorf("%w: presign get object: %v", ErrStorage, err)
	}
	return output.URL, nil
}

func (s *S3Storage) DeleteObject(ctx context.Context, key string) error {
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return fmt.Errorf("%w: delete object: %v", ErrStorage, err)
	}
	return nil
}

// PrefixDeleter is implemented by storage backends that can enumerate and
// delete every object below a key prefix. Media purge requires this capability
// so HLS segment trees cannot be left behind.
type PrefixDeleter interface {
	DeletePrefix(ctx context.Context, prefix string) error
}

func (s *S3Storage) DeletePrefix(ctx context.Context, prefix string) error {
	paginator := s3.NewListObjectsV2Paginator(s.client, &s3.ListObjectsV2Input{
		Bucket: aws.String(s.bucket),
		Prefix: aws.String(prefix),
	})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return fmt.Errorf("%w: list objects: %v", ErrStorage, err)
		}
		if len(page.Contents) == 0 {
			continue
		}
		objects := make([]types.ObjectIdentifier, 0, len(page.Contents))
		for _, object := range page.Contents {
			if object.Key == nil || *object.Key == "" {
				continue
			}
			objects = append(objects, types.ObjectIdentifier{Key: object.Key})
		}
		if len(objects) == 0 {
			continue
		}
		if _, err := s.client.DeleteObjects(ctx, &s3.DeleteObjectsInput{
			Bucket: aws.String(s.bucket),
			Delete: &types.Delete{Objects: objects, Quiet: aws.Bool(true)},
		}); err != nil {
			return fmt.Errorf("%w: delete prefix objects: %v", ErrStorage, err)
		}
	}
	return nil
}
