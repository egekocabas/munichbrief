package web

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"path"
	"strings"
)

const (
	immutableAssetCacheControl  = "public, max-age=31556952, immutable"
	selectedAIGeneratedAsset    = "eu-ai-generated-white-50.svg"
	selectedAILightThemeAsset   = "munichbrief-ai-generated-light.svg"
	selectedAIGeneratedPNGAsset = "eu-ai-generated-white-50.png"
)

type staticAsset struct {
	name        string
	path        string
	contentType string
	content     []byte
}

var euAIAssetNames = []string{
	"eu-ai-generated-black.svg",
	"eu-ai-generated-black-50.svg",
	"eu-ai-generated-white.svg",
	"eu-ai-generated-white-50.svg",
}

var staticAssets = newStaticAssets(embeddedStaticAssets()...)

func embeddedStaticAssets() []staticAsset {
	assets := []staticAsset{
		{name: "app.css", contentType: "text/css; charset=utf-8", content: stylesheet},
		{name: "htmx.min.js", contentType: "text/javascript; charset=utf-8", content: htmxScript},
		{name: "theme.js", contentType: "text/javascript; charset=utf-8", content: themeScript},
		{name: "admin.js", contentType: "text/javascript; charset=utf-8", content: adminScript},
		{name: "favicon.svg", contentType: "image/svg+xml", content: favicon},
		{name: selectedAIGeneratedPNGAsset, contentType: "image/png", content: euAISocialLabel},
	}
	for _, name := range euAIAssetNames {
		content, err := euAIAssetFiles.ReadFile("static/" + name)
		if err != nil {
			panic(fmt.Sprintf("read embedded EU AI asset %q: %v", name, err))
		}
		assets = append(assets, staticAsset{name: name, contentType: "image/svg+xml", content: content})
	}
	lightThemeContent, err := euAIAssetFiles.ReadFile("static/" + selectedAILightThemeAsset)
	if err != nil {
		panic(fmt.Sprintf("read light-theme AI asset %q: %v", selectedAILightThemeAsset, err))
	}
	assets = append(assets, staticAsset{name: selectedAILightThemeAsset, contentType: "image/svg+xml", content: lightThemeContent})
	return assets
}

func newStaticAssets(assets ...staticAsset) map[string]staticAsset {
	result := make(map[string]staticAsset, len(assets)*2)
	for _, asset := range assets {
		digest := sha256.Sum256(asset.content)
		fingerprint := hex.EncodeToString(digest[:])[:12]
		extension := path.Ext(asset.name)
		base := strings.TrimSuffix(asset.name, extension)
		asset.path = "/static/" + base + "." + fingerprint + extension
		result[asset.name] = asset
		result[asset.path] = asset
	}
	return result
}

func assetURL(name string) (string, error) {
	asset, ok := staticAssets[name]
	if !ok {
		return "", fmt.Errorf("unknown static asset %q", name)
	}
	return asset.path, nil
}

// selectedAIGeneratedAssetURL returns the unchanged official label used in
// dark mode and as the basis for the reviewed light-theme derivative.
func selectedAIGeneratedAssetURL() (string, error) {
	return assetURL(selectedAIGeneratedAsset)
}

// selectedAILightThemeAssetURL keeps the custom light-theme treatment shared
// by the disclosure dock and every per-content label.
func selectedAILightThemeAssetURL() (string, error) {
	return assetURL(selectedAILightThemeAsset)
}

func serveStaticAsset(response http.ResponseWriter, request *http.Request) {
	asset, ok := staticAssets[request.URL.Path]
	if !ok {
		http.NotFound(response, request)
		return
	}
	response.Header().Set("Content-Type", asset.contentType)
	response.Header().Set("Cache-Control", immutableAssetCacheControl)
	response.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = response.Write(asset.content)
}
