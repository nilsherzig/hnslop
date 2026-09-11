package hnslop

import _ "embed"

// Static assets are compiled into the server binary so the published server
// always serves the exact versions it was built with.
//
//go:embed index.html
var indexPage string

//go:embed userscript.js
var userscriptAsset []byte

//go:embed hnslop.xpi
var extensionAsset []byte

//go:embed assets/hnslop.png
var screenshotAsset []byte

//go:embed assets/firefox.svg
var firefoxAsset []byte

//go:embed assets/favicon.svg
var faviconAsset []byte
