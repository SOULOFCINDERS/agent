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
    <div class="app">
        <header class="header">
            <div class="header-left">
                <h1>&#x1f916; Agent Chat</h1>
                <span class="status-badge" id="statusBadge">connecting...</span>
            </div>
            <div class="header-right">
                <button class="btn btn-ghost" id="clearBtn" title="清空对话">&#x1f5d1;&#xfe0f; 清空</button>
            </div>
        </header>

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
    <script src="/static/app.js"></script>
</body>
</html>`
}
