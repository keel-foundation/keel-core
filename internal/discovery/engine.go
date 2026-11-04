package discovery

import (
	"context"
	"fmt"
	"log"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/keel-foundation/keel-core/internal/store"
)

type Reason struct {
	Code          string `json:"code"`
	Label         string `json:"label"`
	SourceVideoID string `json:"source_video_id,omitempty"`
	SourceTitle   string `json:"source_title,omitempty"`
	Category      string `json:"category,omitempty"`
	ChannelID     string `json:"channel_id,omitempty"`
}

type RecommendedVideo struct {
	store.Video
	Reason *Reason `json:"reason,omitempty"`
	Score  float64 `json:"score,omitempty"`
}

type FeedResult struct {
	Items        []RecommendedVideo
	Total        int
	Personalized bool
}

type Collection struct {
	ID          string
	Title       string
	Description string
	Reason      *Reason
	Videos      []RecommendedVideo
}

type ExploreResult struct {
	Featured     *RecommendedVideo
	New          []RecommendedVideo
	Trending     []RecommendedVideo
	Collections  []Collection
	Personalized bool
}

type Config struct {
	CacheTTL       time.Duration
	EnsureInterval time.Duration
	MaxCandidates  int
	MaxBatchSize   int
	Now            func() time.Time
}

func DefaultConfig() Config {
	return Config{
		CacheTTL:       time.Hour,
		EnsureInterval: 30 * time.Second,
		MaxCandidates:  200,
		MaxBatchSize:   32,
		Now:            func() time.Time { return time.Now().UTC() },
	}
}

type Engine struct {
	store    *store.Store
	embedder Embedder
	cfg      Config

	ensureMu   sync.Mutex
	lastEnsure time.Time
}

func NewEngine(st *store.Store, embedder Embedder, cfg Config) *Engine {
	defaults := DefaultConfig()
	if cfg.CacheTTL <= 0 {
		cfg.CacheTTL = defaults.CacheTTL
	}
	if cfg.EnsureInterval <= 0 {
		cfg.EnsureInterval = defaults.EnsureInterval
	}
	if cfg.MaxCandidates <= 0 {
		cfg.MaxCandidates = defaults.MaxCandidates
	}
	if cfg.MaxBatchSize <= 0 {
		cfg.MaxBatchSize = defaults.MaxBatchSize
	}
	if cfg.Now == nil {
		cfg.Now = defaults.Now
	}
	return &Engine{store: st, embedder: embedder, cfg: cfg}
}

type signalVector struct {
	Signal store.ActorSignal
	Vector []float32
	Weight float64
}

type profile struct {
	Vector          []float32
	Signals         []signalVector
	RecentSignal    *signalVector
	CategoryWeights map[string]float64
	TopCategory     string
	WatchedAt       map[string]time.Time
	Subscribed      map[string]bool
	Disliked        map[string]bool
}

func (e *Engine) Feed(
	ctx context.Context,
	actorID string,
	limit, offset int,
	format, category string,
) (FeedResult, error) {
	if limit <= 0 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	if actorID == "" {
		return e.coldStartFeed(ctx, limit, offset, format, category)
	}
	if err := e.EnsureVideoEmbeddings(ctx); err != nil {
		log.Printf("discovery: ensure video embeddings: %v", err)
	}
	viewer, err := e.profile(ctx, actorID)
	if err != nil {
		return FeedResult{}, err
	}
	if viewer == nil || len(viewer.Vector) == 0 {
		return e.coldStartFeed(ctx, limit, offset, format, category)
	}

	candidates, err := e.store.NearestVideos(
		ctx,
		viewer.Vector,
		e.embedder.Model(),
		e.cfg.MaxCandidates,
		keys(viewer.Disliked),
	)
	if err != nil {
		return FeedResult{}, err
	}
	filtered := candidates[:0]
	for _, candidate := range candidates {
		if format != "" && candidate.Video.Format != format {
			continue
		}
		if category != "" && candidate.Video.Category != category {
			continue
		}
		filtered = append(filtered, candidate)
	}
	candidates = filtered
	if len(candidates) == 0 {
		return e.coldStartFeed(ctx, limit, offset, format, category)
	}

	maxPopularity := 0.0
	for _, candidate := range candidates {
		if value := popularity(candidate.Video); value > maxPopularity {
			maxPopularity = value
		}
	}
	now := e.now()
	ranked := make([]RecommendedVideo, 0, len(candidates))
	for _, candidate := range candidates {
		video := candidate.Video
		similarity := math.Max(0, candidate.Similarity)
		subscription := 0.0
		if viewer.Subscribed[video.ChannelID] {
			subscription = 1
		}
		affinity := viewer.CategoryWeights[video.Category]
		popularityScore := 0.0
		if maxPopularity > 0 {
			popularityScore = popularity(video) / maxPopularity
		}
		score := 0.55*similarity +
			0.15*subscription +
			0.10*affinity +
			0.10*popularityScore +
			0.10*recencyScore(video, now) -
			watchedPenalty(viewer.WatchedAt[video.ID], now)
		ranked = append(ranked, RecommendedVideo{Video: video, Score: score})
	}
	sort.SliceStable(ranked, func(left, right int) bool {
		if ranked[left].Score == ranked[right].Score {
			return publishedAfter(ranked[left].Video, ranked[right].Video)
		}
		return ranked[left].Score > ranked[right].Score
	})
	ranked = diversify(ranked, e.cfg.MaxCandidates)
	e.attachReasons(ctx, ranked, viewer)

	return FeedResult{
		Items:        paginate(ranked, offset, limit),
		Total:        len(ranked),
		Personalized: true,
	}, nil
}

func (e *Engine) Explore(
	ctx context.Context,
	actorID string,
	limit, collectionLimit int,
) (ExploreResult, error) {
	if limit <= 0 {
		limit = 8
	}
	if collectionLimit <= 0 {
		collectionLimit = 4
	}
	if actorID == "" {
		return ExploreResult{Personalized: false}, nil
	}
	if err := e.EnsureVideoEmbeddings(ctx); err != nil {
		log.Printf("discovery: ensure video embeddings: %v", err)
	}
	viewer, err := e.profile(ctx, actorID)
	if err != nil {
		return ExploreResult{}, err
	}
	if viewer == nil || len(viewer.Vector) == 0 {
		return ExploreResult{Personalized: false}, nil
	}

	result := ExploreResult{Personalized: true}

	newVideos, _, err := e.store.ListVideos(ctx, store.VideoFilter{
		PublicOnly: true,
		Format:     "video",
		Sort:       "latest",
		Limit:      limit,
	})
	if err != nil {
		return ExploreResult{}, err
	}
	result.New = wrapVideos(newVideos, &Reason{Code: "new", Label: "New on KeelTube"})

	trendingVideos, _, err := e.store.ListVideos(ctx, store.VideoFilter{
		PublicOnly: true,
		Format:     "video",
		Sort:       "trending",
		Limit:      limit,
	})
	if err != nil {
		return ExploreResult{}, err
	}
	result.Trending = wrapVideos(trendingVideos, &Reason{Code: "trending", Label: "Popular now"})

	if recent := e.recentlyWatched(ctx, viewer, collectionLimit); len(recent) > 0 {
		result.Collections = append(result.Collections, Collection{
			ID:          "recently-watched",
			Title:       "Recently watched",
			Description: "Pick up where you left off.",
			Reason:      &Reason{Code: "recently_watched", Label: "Recently watched"},
			Videos:      recent,
		})
	}

	now := e.now()
	if viewer.RecentSignal != nil &&
		viewer.RecentSignal.Signal.Kind != "subscription" &&
		len(viewer.RecentSignal.Vector) > 0 {
		similar, err := e.store.NearestVideos(
			ctx,
			viewer.RecentSignal.Vector,
			e.embedder.Model(),
			e.cfg.MaxCandidates,
			keys(viewer.Disliked),
		)
		if err != nil {
			return ExploreResult{}, err
		}
		similar = filterSimilar(similar, viewer.RecentSignal.Signal.VideoID, viewer.WatchedAt, now)
		code, label := signalReason(
			viewer.RecentSignal.Signal.Kind,
			viewer.RecentSignal.Signal.Title,
		)
		reason := &Reason{
			Code:          code,
			Label:         label,
			SourceVideoID: viewer.RecentSignal.Signal.VideoID,
			SourceTitle:   viewer.RecentSignal.Signal.Title,
		}
		videos := wrapSimilar(similar, collectionLimit, reason)
		if len(videos) > 0 {
			result.Collections = append(result.Collections, Collection{
				ID:          "because-you-watched",
				Title:       label,
				Description: "More like the videos you have been watching.",
				Reason:      reason,
				Videos:      videos,
			})
		}
	}

	if viewer.TopCategory != "" {
		categoryVideos, _, err := e.store.ListVideos(ctx, store.VideoFilter{
			PublicOnly: true,
			Categories: []string{viewer.TopCategory},
			Sort:       "trending",
			Limit:      collectionLimit * 3,
		})
		if err != nil {
			return ExploreResult{}, err
		}
		categoryVideos = filterWatchedVideos(categoryVideos, viewer.WatchedAt, collectionLimit, now)
		if len(categoryVideos) > 0 {
			result.Collections = append(result.Collections, Collection{
				ID:          "more-in-category",
				Title:       "More in " + viewer.TopCategory,
				Description: "Popular videos in the category you watch most.",
				Reason: &Reason{
					Code:     "category",
					Label:    "More in " + viewer.TopCategory,
					Category: viewer.TopCategory,
				},
				Videos: wrapVideos(categoryVideos, &Reason{
					Code:     "category",
					Label:    "More in " + viewer.TopCategory,
					Category: viewer.TopCategory,
				}),
			})
		}
	}

	subscriptionVideos, _, err := e.store.ListVideos(ctx, store.VideoFilter{
		SubscribedBy: actorID,
		PublicOnly:   true,
		Format:       "video",
		Sort:         "latest",
		Limit:        collectionLimit,
	})
	if err != nil {
		return ExploreResult{}, err
	}
	if len(subscriptionVideos) > 0 {
		reason := &Reason{Code: "subscription", Label: "New from your subscriptions"}
		result.Collections = append(result.Collections, Collection{
			ID:          "new-from-subscriptions",
			Title:       "New from your subscriptions",
			Description: "The latest videos from channels you follow.",
			Reason:      reason,
			Videos:      wrapVideos(subscriptionVideos, reason),
		})
	}

	for index := range result.Collections {
		if len(result.Collections[index].Videos) == 0 {
			continue
		}
		featured := result.Collections[index].Videos[0]
		result.Featured = &featured
		break
	}
	if result.Featured == nil && len(result.Trending) > 0 {
		result.Featured = &result.Trending[0]
	}
	return result, nil
}

func (e *Engine) InvalidateActor(ctx context.Context, actorID string) error {
	return e.store.DeleteActorEmbedding(ctx, actorID, e.embedder.Model())
}

func (e *Engine) RefreshVideo(ctx context.Context, videoID string) error {
	video, err := e.store.GetVideo(ctx, videoID)
	if err != nil {
		return err
	}
	channel, err := e.store.GetChannel(ctx, video.ChannelID)
	if err != nil {
		return err
	}
	vectors, err := e.embedder.Embed(ctx, []string{videoText(video, channel.Name)}, InputTypePassage)
	if err != nil {
		return err
	}
	if len(vectors) != 1 {
		return fmt.Errorf("embedder returned %d vectors for one video", len(vectors))
	}
	if len(vectors[0]) != e.embedder.Dimensions() {
		return fmt.Errorf(
			"embedder returned %d dimensions; expected %d",
			len(vectors[0]),
			e.embedder.Dimensions(),
		)
	}
	return e.store.UpsertVideoEmbedding(
		ctx,
		video.ID,
		e.embedder.Model(),
		e.embedder.Dimensions(),
		vectors[0],
		video.ContentHash,
		e.now(),
	)
}

func (e *Engine) RefreshAllVideos(ctx context.Context) error {
	return e.refreshVideos(ctx, true)
}

func (e *Engine) EnsureVideoEmbeddings(ctx context.Context) error {
	return e.refreshVideos(ctx, false)
}

func (e *Engine) refreshVideos(ctx context.Context, force bool) error {
	e.ensureMu.Lock()
	defer e.ensureMu.Unlock()

	now := e.now()
	if !force && !e.lastEnsure.IsZero() && now.Sub(e.lastEnsure) < e.cfg.EnsureInterval {
		return nil
	}
	sources, err := e.store.ListVideosForEmbedding(ctx, e.embedder.Model())
	if err != nil {
		return err
	}
	pending := make([]store.VideoEmbeddingSource, 0, len(sources))
	for _, source := range sources {
		if force ||
			source.EmbeddingUpdatedAt == nil ||
			source.EmbeddingHash != source.Video.ContentHash {
			pending = append(pending, source)
		}
	}
	for start := 0; start < len(pending); start += e.cfg.MaxBatchSize {
		end := start + e.cfg.MaxBatchSize
		if end > len(pending) {
			end = len(pending)
		}
		batch := pending[start:end]
		texts := make([]string, len(batch))
		for index, source := range batch {
			texts[index] = videoText(source.Video, source.ChannelName)
		}
		vectors, err := e.embedder.Embed(ctx, texts, InputTypePassage)
		if err != nil {
			return err
		}
		if len(vectors) != len(batch) {
			return fmt.Errorf(
				"embedder returned %d vectors for %d videos",
				len(vectors),
				len(batch),
			)
		}
		for index, source := range batch {
			if len(vectors[index]) != e.embedder.Dimensions() {
				return fmt.Errorf(
					"embedder returned %d dimensions; expected %d",
					len(vectors[index]),
					e.embedder.Dimensions(),
				)
			}
			if err := e.store.UpsertVideoEmbedding(
				ctx,
				source.Video.ID,
				e.embedder.Model(),
				e.embedder.Dimensions(),
				vectors[index],
				source.Video.ContentHash,
				now,
			); err != nil {
				return err
			}
		}
	}
	e.lastEnsure = now
	return nil
}

func (e *Engine) profile(ctx context.Context, actorID string) (*profile, error) {
	signals, err := e.store.ListActorSignals(ctx, actorID, 100)
	if err != nil {
		return nil, err
	}
	subscriptions, err := e.store.ListSubscribedChannelIDs(ctx, actorID)
	if err != nil {
		return nil, err
	}
	dislikedIDs, err := e.store.ListDislikedVideoIDs(ctx, actorID)
	if err != nil {
		return nil, err
	}
	if len(signals) == 0 && len(subscriptions) == 0 {
		return nil, nil
	}

	viewer := &profile{
		CategoryWeights: make(map[string]float64),
		WatchedAt:       make(map[string]time.Time),
		Subscribed:      make(map[string]bool, len(subscriptions)),
		Disliked:        make(map[string]bool, len(dislikedIDs)),
	}
	for _, channelID := range subscriptions {
		viewer.Subscribed[channelID] = true
	}
	for _, videoID := range dislikedIDs {
		viewer.Disliked[videoID] = true
	}

	now := e.now()
	signalIDs := make([]string, 0, len(signals))
	seen := make(map[string]bool, len(signals))
	for _, signal := range signals {
		if !seen[signal.VideoID] {
			seen[signal.VideoID] = true
			signalIDs = append(signalIDs, signal.VideoID)
		}
	}
	embeddings, err := e.store.GetVideoEmbeddings(ctx, signalIDs, e.embedder.Model())
	if err != nil {
		return nil, err
	}
	accumulator := make([]float64, e.embedder.Dimensions())
	for _, signal := range signals {
		vector, ok := embeddings[signal.VideoID]
		if !ok || len(vector) != len(accumulator) {
			continue
		}
		weight := signalWeight(signal, now)
		if weight <= 0 {
			continue
		}
		for index, value := range vector {
			accumulator[index] += float64(value) * weight
		}
		viewer.Signals = append(viewer.Signals, signalVector{
			Signal: signal,
			Vector: vector,
			Weight: weight,
		})
		viewer.CategoryWeights[signal.Category] += weight
		if signal.Kind == "watch" {
			if current, ok := viewer.WatchedAt[signal.VideoID]; !ok || signal.At.After(current) {
				viewer.WatchedAt[signal.VideoID] = signal.At
			}
		}
		if viewer.RecentSignal == nil && signal.Kind == "watch" {
			candidate := viewer.Signals[len(viewer.Signals)-1]
			viewer.RecentSignal = &candidate
		}
	}

	if len(viewer.Signals) == 0 && len(subscriptions) > 0 {
		subscriptionVideos, _, err := e.store.ListVideos(ctx, store.VideoFilter{
			SubscribedBy: actorID,
			PublicOnly:   true,
			Format:       "video",
			Sort:         "latest",
			Limit:        20,
		})
		if err != nil {
			return nil, err
		}
		ids := make([]string, 0, len(subscriptionVideos))
		for _, video := range subscriptionVideos {
			ids = append(ids, video.ID)
		}
		embeddings, err := e.store.GetVideoEmbeddings(ctx, ids, e.embedder.Model())
		if err != nil {
			return nil, err
		}
		for _, video := range subscriptionVideos {
			vector, ok := embeddings[video.ID]
			if !ok || len(vector) != len(accumulator) {
				continue
			}
			weight := 0.5
			for index, value := range vector {
				accumulator[index] += float64(value) * weight
			}
			viewer.Signals = append(viewer.Signals, signalVector{
				Signal: store.ActorSignal{
					Kind:     "subscription",
					VideoID:  video.ID,
					Title:    video.Title,
					Category: video.Category,
					At:       now,
				},
				Vector: vector,
				Weight: weight,
			})
			viewer.CategoryWeights[video.Category] += weight
		}
	}

	if viewer.RecentSignal == nil && len(viewer.Signals) > 0 {
		candidate := viewer.Signals[0]
		viewer.RecentSignal = &candidate
	}
	viewer.Vector = normalize(accumulator)
	if len(viewer.Vector) == 0 {
		return nil, nil
	}

	totalWeight := 0.0
	for _, weight := range viewer.CategoryWeights {
		totalWeight += weight
	}
	if totalWeight > 0 {
		topWeight := 0.0
		for category, weight := range viewer.CategoryWeights {
			viewer.CategoryWeights[category] = weight / totalWeight
			if viewer.CategoryWeights[category] > topWeight {
				topWeight = viewer.CategoryWeights[category]
				viewer.TopCategory = category
			}
		}
	}

	cached, updatedAt, found, err := e.store.GetActorEmbedding(
		ctx, actorID, e.embedder.Model(),
	)
	if err != nil {
		return nil, err
	}
	if found && now.Sub(updatedAt) < e.cfg.CacheTTL {
		viewer.Vector = cached
	} else if err := e.store.UpsertActorEmbedding(
		ctx,
		actorID,
		e.embedder.Model(),
		e.embedder.Dimensions(),
		viewer.Vector,
		now,
	); err != nil {
		return nil, err
	}
	return viewer, nil
}

func (e *Engine) coldStartFeed(
	ctx context.Context,
	limit, offset int,
	format, category string,
) (FeedResult, error) {
	filter := store.VideoFilter{
		PublicOnly: true,
		Format:     format,
		Sort:       "trending",
		Limit:      e.cfg.MaxCandidates,
	}
	if category != "" {
		filter.Categories = []string{category}
	}
	trending, _, err := e.store.ListVideos(ctx, filter)
	if err != nil {
		return FeedResult{}, err
	}
	filter.Sort = "latest"
	latest, _, err := e.store.ListVideos(ctx, filter)
	if err != nil {
		return FeedResult{}, err
	}

	merged := mergeColdStart(trending, latest)
	now := e.now()
	trendingIDs := make(map[string]bool, len(trending))
	for _, video := range trending {
		trendingIDs[video.ID] = true
	}
	ranked := make([]RecommendedVideo, 0, len(merged))
	for _, video := range merged {
		reason := &Reason{Code: "trending", Label: "Popular now"}
		if !trendingIDs[video.ID] {
			reason = &Reason{Code: "new", Label: "New on KeelTube"}
		}
		ranked = append(ranked, RecommendedVideo{
			Video:  video,
			Reason: reason,
			Score:  recencyScore(video, now),
		})
	}
	ranked = diversify(ranked, e.cfg.MaxCandidates)
	return FeedResult{
		Items:        paginate(ranked, offset, limit),
		Total:        len(ranked),
		Personalized: false,
	}, nil
}

func (e *Engine) recentlyWatched(
	ctx context.Context,
	viewer *profile,
	limit int,
) []RecommendedVideo {
	videos := make([]RecommendedVideo, 0, limit)
	seen := make(map[string]bool)
	for _, signal := range viewer.Signals {
		if signal.Signal.Kind != "watch" || seen[signal.Signal.VideoID] {
			continue
		}
		seen[signal.Signal.VideoID] = true
		video, err := e.store.GetVideo(ctx, signal.Signal.VideoID)
		if err != nil || video.Status == "removed" {
			continue
		}
		videos = append(videos, RecommendedVideo{
			Video:  video,
			Reason: &Reason{Code: "recently_watched", Label: "Recently watched"},
		})
		if len(videos) >= limit {
			break
		}
	}
	return videos
}

func (e *Engine) attachReasons(
	ctx context.Context,
	videos []RecommendedVideo,
	viewer *profile,
) {
	if len(videos) == 0 {
		return
	}
	ids := make([]string, 0, len(videos))
	for _, video := range videos {
		ids = append(ids, video.ID)
	}
	embeddings, err := e.store.GetVideoEmbeddings(ctx, ids, e.embedder.Model())
	if err != nil {
		log.Printf("discovery: load candidate embeddings: %v", err)
	}
	for index := range videos {
		video := &videos[index]
		if viewer.Subscribed[video.ChannelID] {
			video.Reason = &Reason{
				Code:      "subscription",
				Label:     "From your subscriptions",
				ChannelID: video.ChannelID,
			}
			continue
		}
		vector, ok := embeddings[video.ID]
		bestScore := 0.0
		var best *signalVector
		if ok {
			for signalIndex := range viewer.Signals {
				signal := &viewer.Signals[signalIndex]
				if signal.Signal.VideoID == video.ID || len(signal.Vector) != len(vector) {
					continue
				}
				score := cosine(vector, signal.Vector)
				if score > bestScore {
					bestScore = score
					best = signal
				}
			}
		}
		if best != nil && bestScore >= 0.45 && best.Signal.Kind != "subscription" {
			code, label := signalReason(best.Signal.Kind, best.Signal.Title)
			video.Reason = &Reason{
				Code:          code,
				Label:         label,
				SourceVideoID: best.Signal.VideoID,
				SourceTitle:   best.Signal.Title,
			}
			continue
		}
		if affinity := viewer.CategoryWeights[video.Category]; affinity > 0.2 {
			video.Reason = &Reason{
				Code:     "category",
				Label:    "More in " + video.Category,
				Category: video.Category,
			}
			continue
		}
		video.Reason = &Reason{Code: "recommended", Label: "Recommended for you"}
	}
}

func (e *Engine) now() time.Time {
	return e.cfg.Now()
}

func videoText(video store.Video, channelName string) string {
	return strings.TrimSpace(fmt.Sprintf(
		"%s\n%s\nCategory: %s\nTags: %s\nChannel: %s",
		video.Title,
		video.Description,
		video.Category,
		strings.Join(video.Tags, ", "),
		channelName,
	))
}

func signalReason(kind, title string) (string, string) {
	switch kind {
	case "like":
		return "because_you_liked", "Because you liked " + title
	case "save":
		return "because_you_saved", "Because you saved " + title
	default:
		return "because_you_watched", "Because you watched " + title
	}
}

func signalWeight(signal store.ActorSignal, now time.Time) float64 {
	ageDays := now.Sub(signal.At).Hours() / 24
	if ageDays < 0 {
		ageDays = 0
	}
	switch signal.Kind {
	case "watch":
		return math.Exp(-ageDays / 14)
	case "like":
		return 1.5 * math.Exp(-ageDays/60)
	case "save":
		return 1.25 * math.Exp(-ageDays/60)
	case "comment":
		return 0.9 * math.Exp(-ageDays/30)
	case "click":
		return 0.7 * math.Exp(-ageDays/14)
	default:
		return 0
	}
}

func popularity(video store.Video) float64 {
	return math.Log1p(float64(video.Views)) +
		2*math.Log1p(float64(video.Likes)) +
		3*math.Log1p(float64(video.CommentsCount))
}

func recencyScore(video store.Video, now time.Time) float64 {
	published := video.CreatedAt
	if video.PublishedAt != nil {
		published = *video.PublishedAt
	}
	ageDays := now.Sub(published).Hours() / 24
	if ageDays < 0 {
		ageDays = 0
	}
	return math.Exp(-ageDays / 30)
}

func watchedPenalty(watchedAt time.Time, now time.Time) float64 {
	if watchedAt.IsZero() {
		return 0
	}
	ageDays := now.Sub(watchedAt).Hours() / 24
	switch {
	case ageDays <= 7:
		return 0.25
	case ageDays <= 30:
		return 0.1
	default:
		return 0
	}
}

func publishedAfter(left, right store.Video) bool {
	leftTime := left.CreatedAt
	if left.PublishedAt != nil {
		leftTime = *left.PublishedAt
	}
	rightTime := right.CreatedAt
	if right.PublishedAt != nil {
		rightTime = *right.PublishedAt
	}
	return leftTime.After(rightTime)
}

func diversify(videos []RecommendedVideo, max int) []RecommendedVideo {
	if max <= 0 || max > len(videos) {
		max = len(videos)
	}
	selected := make([]RecommendedVideo, 0, max)
	skipped := make([]RecommendedVideo, 0)
	channelCounts := make(map[string]int)
	categoryCounts := make(map[string]int)
	for _, video := range videos {
		if len(selected) >= max {
			break
		}
		if channelCounts[video.ChannelID] >= 2 || categoryCounts[video.Category] >= 4 {
			skipped = append(skipped, video)
			continue
		}
		selected = append(selected, video)
		channelCounts[video.ChannelID]++
		categoryCounts[video.Category]++
	}
	for _, video := range skipped {
		if len(selected) >= max {
			break
		}
		selected = append(selected, video)
	}
	return selected
}

func mergeColdStart(trending, latest []store.Video) []store.Video {
	merged := make([]store.Video, 0, len(trending)+len(latest))
	seen := make(map[string]bool, len(trending)+len(latest))
	trendingLimit := len(trending) * 2 / 3
	for index, video := range trending {
		if index >= trendingLimit {
			break
		}
		if seen[video.ID] {
			continue
		}
		seen[video.ID] = true
		merged = append(merged, video)
	}
	latestLimit := len(latest) / 3
	if latestLimit < 1 {
		latestLimit = 1
	}
	for index, video := range latest {
		if index >= latestLimit {
			break
		}
		if seen[video.ID] {
			continue
		}
		seen[video.ID] = true
		merged = append(merged, video)
	}
	for _, list := range [][]store.Video{trending, latest} {
		for _, video := range list {
			if seen[video.ID] {
				continue
			}
			seen[video.ID] = true
			merged = append(merged, video)
		}
	}
	return merged
}

func paginate(videos []RecommendedVideo, offset, limit int) []RecommendedVideo {
	if offset >= len(videos) {
		return []RecommendedVideo{}
	}
	end := offset + limit
	if end > len(videos) {
		end = len(videos)
	}
	return videos[offset:end]
}

func wrapVideos(videos []store.Video, reason *Reason) []RecommendedVideo {
	result := make([]RecommendedVideo, len(videos))
	for index, video := range videos {
		result[index] = RecommendedVideo{Video: video, Reason: reason}
	}
	return result
}

func wrapSimilar(
	similar []store.VideoSimilarity,
	limit int,
	reason *Reason,
) []RecommendedVideo {
	result := make([]RecommendedVideo, 0, limit)
	for _, candidate := range similar {
		if len(result) >= limit {
			break
		}
		result = append(result, RecommendedVideo{
			Video:  candidate.Video,
			Reason: reason,
			Score:  candidate.Similarity,
		})
	}
	return result
}

func filterSimilar(
	similar []store.VideoSimilarity,
	excludeID string,
	watchedAt map[string]time.Time,
	now time.Time,
) []store.VideoSimilarity {
	result := similar[:0]
	cutoff := now.Add(-30 * 24 * time.Hour)
	for _, candidate := range similar {
		if candidate.Video.ID == excludeID {
			continue
		}
		if watched, ok := watchedAt[candidate.Video.ID]; ok && watched.After(cutoff) {
			continue
		}
		result = append(result, candidate)
	}
	return result
}

func filterWatchedVideos(
	videos []store.Video,
	watchedAt map[string]time.Time,
	limit int,
	now time.Time,
) []store.Video {
	result := make([]store.Video, 0, limit)
	cutoff := now.Add(-30 * 24 * time.Hour)
	for _, video := range videos {
		if watched, ok := watchedAt[video.ID]; ok && watched.After(cutoff) {
			continue
		}
		result = append(result, video)
		if len(result) >= limit {
			break
		}
	}
	return result
}

func keys(values map[string]bool) []string {
	result := make([]string, 0, len(values))
	for key := range values {
		result = append(result, key)
	}
	return result
}

func cosine(left, right []float32) float64 {
	if len(left) != len(right) || len(left) == 0 {
		return 0
	}
	var sum float64
	for index := range left {
		sum += float64(left[index]) * float64(right[index])
	}
	return math.Max(-1, math.Min(1, sum))
}

func normalize(vector []float64) []float32 {
	var sum float64
	for _, value := range vector {
		sum += value * value
	}
	if sum == 0 {
		return nil
	}
	norm := math.Sqrt(sum)
	result := make([]float32, len(vector))
	for index, value := range vector {
		result[index] = float32(value / norm)
	}
	return result
}

func (e *Engine) EmbeddingModel() string {
	if e == nil || e.embedder == nil {
		return ""
	}
	return e.embedder.Model()
}

func (e *Engine) EmbedQuery(ctx context.Context, text string) ([]float32, error) {
	if e == nil || e.embedder == nil {
		return nil, fmt.Errorf("discovery embedder is not configured")
	}
	vectors, err := e.embedder.Embed(ctx, []string{text}, InputTypeQuery)
	if err != nil {
		return nil, err
	}
	if len(vectors) != 1 {
		return nil, fmt.Errorf("embedder returned %d vectors for one query", len(vectors))
	}
	if len(vectors[0]) != e.embedder.Dimensions() {
		return nil, fmt.Errorf(
			"embedder returned %d dimensions; expected %d",
			len(vectors[0]), e.embedder.Dimensions(),
		)
	}
	return vectors[0], nil
}
