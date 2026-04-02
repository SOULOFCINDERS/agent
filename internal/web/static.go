package web

// 所有前端内容通过 init() 拼接，避免 Go raw string 无法包含反引号的问题

var IndexHTML string
var StyleCSS string
var AppJS string

func init() {
	IndexHTML = buildIndexHTML()
	StyleCSS = buildStyleCSS()
	AppJS = buildAppJS()
}
