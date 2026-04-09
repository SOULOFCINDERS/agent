package web

import _ "embed"

//go:embed static/index.html
var IndexHTML string

//go:embed static/style.css
var StyleCSS string

//go:embed static/app.js
var AppJS string
