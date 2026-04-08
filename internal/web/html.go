package web

func buildIndexHTML() string {
	return `<!DOCTYPE html>
<html lang="zh-CN">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Agent Chat</title>
    <link rel="stylesheet" href="/static/style.css">
</head>
<body>
    <div class="layout">
        <!-- 侧边栏：历史对话 -->
        <aside class="sidebar" id="sidebar">
            <div class="sidebar-header">
                <h2>&#x1f4ac; 对话历史</h2>
                <button class="btn btn-ghost sidebar-close" id="sidebarClose" title="收起侧边栏">&#x2715;</button>
            </div>
            <button class="new-chat-btn" id="newChatBtn">&#x2795; 新对话</button>
            <div class="session-list" id="sessionList">
                <!-- 动态渲染 -->
            </div>
        </aside>

        <!-- 主区域 -->
        <div class="app">
            <header class="header">
                <div class="header-left">
                    <button class="btn btn-ghost" id="sidebarToggle" title="对话历史">&#x2630;</button>
                    <h1>&#x1f916; Agent Chat</h1>
                    <span class="status-badge" id="statusBadge">connecting...</span>
                </div>
                <div class="header-right">
                    <button class="btn btn-ghost" id="toggleStats" title="显示/隐藏用量面板">&#x1f4ca;</button>
                    <button class="btn btn-ghost" id="clearBtn" title="清空对话">&#x1f5d1;&#xfe0f; 清空</button>
                </div>
            </header>

            <div class="context-bar" id="contextBar">
                <div class="ctx-section">
                    <div class="ctx-label">
                        <span>上下文窗口</span>
                        <span class="ctx-ratio" id="ctxRatio">0 / 0 tokens</span>
                    </div>
                    <div class="ctx-progress-track">
                        <div class="ctx-progress-fill" id="ctxProgressFill"></div>
                    </div>
                    <div class="ctx-meta">
                        <span id="ctxPercent">0%</span>
                        <span id="ctxMessages">0 条消息</span>
                        <span id="ctxRemaining">剩余 0 tokens</span>
                    </div>
                </div>
                <div class="ctx-divider"></div>
                <div class="ctx-section">
                    <div class="ctx-label">
                        <span>Token 用量</span>
                        <span class="ctx-budget" id="ctxBudget"></span>
                    </div>
                    <div class="ctx-token-grid">
                        <div class="ctx-token-item">
                            <span class="ctx-token-label">总计</span>
                            <span class="ctx-token-value" id="tkTotal">0</span>
                        </div>
                        <div class="ctx-token-item">
                            <span class="ctx-token-label">输入</span>
                            <span class="ctx-token-value" id="tkPrompt">0</span>
                        </div>
                        <div class="ctx-token-item">
                            <span class="ctx-token-label">输出</span>
                            <span class="ctx-token-value" id="tkCompletion">0</span>
                        </div>
                        <div class="ctx-token-item">
                            <span class="ctx-token-label">调用</span>
                            <span class="ctx-token-value" id="tkCalls">0</span>
                        </div>
                    </div>
                </div>
            </div>

            <main class="chat-container" id="chatContainer">
                <div class="welcome-msg">
                    <div class="welcome-icon">&#x1f916;</div>
                    <h2>你好！我是 Agent 智能助手</h2>
                    <p>我可以帮你搜索信息、读取文件、计算、查天气，还能操作飞书文档。</p>
                    <div class="quick-actions">
                        <button class="quick-btn" onclick="sendQuick('今天有什么科技新闻？')">&#x1f50d; 科技新闻</button>
                        <button class="quick-btn" onclick="sendQuick('列出当前目录')">&#x1f4c2; 列出目录</button>
                        <button class="quick-btn" onclick="sendQuick('计算 (123+456)*789')">&#x1f9ee; 计算</button>
                        <button class="quick-btn" onclick="sendQuick('北京天气')">&#x1f324;&#xfe0f; 天气</button>
                    </div>
                </div>
            </main>

            <footer class="input-area">
                <div class="input-wrapper">
                    <textarea id="messageInput" placeholder="输入消息... (Enter 发送, Shift+Enter 换行)" rows="1"></textarea>
                    <button class="send-btn" id="sendBtn" title="发送">
                        <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M22 2L11 13"/><path d="M22 2L15 22L11 13L2 9L22 2Z"/></svg>
                    </button>
                </div>
            </footer>
        </div>
    </div>
    <script src="/static/app.js"></script>
</body>
</html>`
}
