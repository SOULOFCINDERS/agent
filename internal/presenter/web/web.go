// Package web 是 Presenter 层的 Web 实现。
// 当前阶段为转发层，实际逻辑仍在 internal/web 中。
// 后续将把 HTTP handler 逻辑迁入此包，internal/web 仅作为兼容层。
package web

import (
	origweb "github.com/SOULOFCINDERS/agent/internal/web"
)

// Server Web 服务器（转发到 internal/web）
type Server = origweb.Server

// ServerConfig 服务器配置（转发到 internal/web）
type ServerConfig = origweb.ServerConfig

// NewServer 创建 Web 服务器
var NewServer = origweb.NewServer
