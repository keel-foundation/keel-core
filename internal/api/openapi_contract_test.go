package api_test

import (
	"bufio"
	"os"
	"regexp"
	"strings"
	"testing"
)

var coreRoutePattern = regexp.MustCompile(`"(GET|POST|PUT|PATCH|DELETE) (/[^"]+)"`)
var openAPIPathPattern = regexp.MustCompile(`^  (/[^:\s]+):\s*$`)
var openAPIMethodPattern = regexp.MustCompile(`^    (get|post|put|patch|delete):\s*$`)

func TestOpenAPICoversEveryCoreRoute(t *testing.T) {
	source, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatalf("read server.go: %v", err)
	}
	openAPI, err := os.ReadFile("../../../keel-spec/schemas/openapi.yaml")
	if os.IsNotExist(err) {
		t.Skip("keel-spec is not checked out as a sibling repository")
	}
	if err != nil {
		t.Fatalf("read openapi.yaml: %v", err)
	}

	paths := make(map[string]struct{})
	operations := make(map[string]struct{})
	scanner := bufio.NewScanner(strings.NewReader(string(openAPI)))
	currentPath := ""
	for scanner.Scan() {
		line := scanner.Text()
		if match := openAPIPathPattern.FindStringSubmatch(line); match != nil {
			currentPath = match[1]
			paths[currentPath] = struct{}{}
			continue
		}
		if match := openAPIMethodPattern.FindStringSubmatch(line); match != nil && currentPath != "" {
			operations[strings.ToUpper(match[1])+" "+currentPath] = struct{}{}
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan openapi.yaml: %v", err)
	}

	coreRoutes := make(map[string]struct{})
	for _, match := range coreRoutePattern.FindAllStringSubmatch(string(source), -1) {
		route := match[1] + " " + match[2]
		coreRoutes[route] = struct{}{}
		if _, ok := operations[route]; !ok {
			t.Errorf("core route %s is missing from keel-spec OpenAPI", route)
		}
	}

	for path := range paths {
		if !strings.HasPrefix(path, "/api/v1/") && path != "/health" {
			continue
		}
		registered := false
		for route := range coreRoutes {
			if strings.HasSuffix(route, " "+path) {
				registered = true
				break
			}
		}
		if !registered {
			t.Errorf("OpenAPI path %s is not registered by core", path)
		}
	}
}
