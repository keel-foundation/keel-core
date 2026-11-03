package store

var Licenses = []string{"standard", "cc-by", "cc-by-sa", "cc0", "all-rights-reserved"}

var Visibilities = []string{"public", "unlisted", "private"}

var VideoStatuses = []string{"draft", "processing", "published", "removed", "demonetized"}

func ValidVisibility(value string) bool {
	return contains(Visibilities, value)
}

func ValidCategory(value string) bool {
	return contains(Categories, value)
}

func ValidLicense(value string) bool {
	return contains(Licenses, value)
}

func ValidFormat(value string) bool {
	return contains(Formats, value)
}

func ValidVideoStatus(value string) bool {
	return contains(VideoStatuses, value)
}

func HasControlCharacters(value string) bool {
	for _, r := range value {
		if r == '\n' || r == '\r' || r == '\t' {
			continue
		}
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
