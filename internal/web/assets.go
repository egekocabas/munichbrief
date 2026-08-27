package web

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"path"
	"strings"
)

const immutableAssetCacheControl = "public, max-age=31556952, immutable"

type staticAsset struct {
	name        string
	path        string
	contentType string
	content     []byte
}

var staticAssets = newStaticAssets(
	staticAsset{name: "app.css", contentType: "text/css; charset=utf-8", content: stylesheet},
	staticAsset{name: "htmx.min.js", contentType: "text/javascript; charset=utf-8", content: htmxScript},
	staticAsset{name: "theme.js", contentType: "text/javascript; charset=utf-8", content: themeScript},
	staticAsset{name: "admin.js", contentType: "text/javascript; charset=utf-8", content: adminScript},
	staticAsset{name: "favicon.svg", contentType: "image/svg+xml", content: favicon},
)

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
