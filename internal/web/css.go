package web

func buildStyleCSS() string {
	return `
* { margin: 0; padding: 0; box-sizing: border-box; }

:root {
    --bg: #0f0f0f;
    --surface: #1a1a1a;
    --surface2: #242424;
    --border: #333;
    --text: #e0e0e0;
    --text-dim: #888;
    --accent: #4f8ff7;
    --accent-hover: #3a7be0;
    --user-bg: #1a3a5c;
    --agent-bg: #1e1e1e;
    --success: #4caf50;
    --error: #ef5350;
    --radius: 12px;
}

body {
    font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
    background: var(--bg);
    color: var(--text);
    height: 100vh;
    overflow: hidden;
}

.app {
    display: flex;
    flex-direction: column;
    height: 100vh;
    max-width: 900px;
    margin: 0 auto;
}

.header {
    display: flex;
    justify-content: space-between;
    align-items: center;
    padding: 16px 20px;
    border-bottom: 1px solid var(--border);
    background: var(--surface);
}
.header-left { display: flex; align-items: center; gap: 12px; }
.header h1 { font-size: 18px; font-weight: 600; }
.status-badge {
    font-size: 11px; padding: 3px 8px; border-radius: 10px;
    background: var(--surface2); color: var(--text-dim);
}
.status-badge.ok { background: #1b3a1b; color: var(--success); }
.status-badge.err { background: #3a1b1b; color: var(--error); }
.btn { border: none; cursor: pointer; font-size: 14px; border-radius: 8px; padding: 6px 12px; transition: all .15s; }
.btn-ghost { background: transparent; color: var(--text-dim); }
.btn-ghost:hover { background: var(--surface2); color: var(--text); }

.chat-container {
    flex: 1;
    overflow-y: auto;
    padding: 20px;
    scroll-behavior: smooth;
}
.welcome-msg { text-align: center; padding: 60px 20px; }
.welcome-icon { font-size: 48px; margin-bottom: 16px; }
.welcome-msg h2 { font-size: 22px; margin-bottom: 8px; }
.welcome-msg p { color: var(--text-dim); margin-bottom: 24px; }
.quick-actions { display: flex; gap: 8px; justify-content: center; flex-wrap: wrap; }
.quick-btn {
    background: var(--surface2); border: 1px solid var(--border);
    color: var(--text); padding: 8px 16px; border-radius: 20px;
    cursor: pointer; font-size: 13px; transition: all .15s;
}
.quick-btn:hover { border-color: var(--accent); color: var(--accent); }

.message { display: flex; gap: 12px; margin-bottom: 20px; animation: fadeIn .3s ease; }
@keyframes fadeIn { from { opacity: 0; transform: translateY(8px); } to { opacity: 1; transform: none; } }
.message.user { flex-direction: row-reverse; }
.msg-avatar {
    width: 36px; height: 36px; border-radius: 10px;
    display: flex; align-items: center; justify-content: center;
    font-size: 18px; flex-shrink: 0;
}
.message.user .msg-avatar { background: var(--user-bg); }
.message.agent .msg-avatar { background: var(--surface2); }
.msg-content {
    max-width: 75%; padding: 12px 16px; border-radius: var(--radius);
    line-height: 1.6; font-size: 14px; word-break: break-word;
}
.message.user .msg-content { background: var(--user-bg); border-bottom-right-radius: 4px; }
.message.agent .msg-content { background: var(--agent-bg); border: 1px solid var(--border); border-bottom-left-radius: 4px; }
.msg-content pre {
    background: #111; padding: 12px; border-radius: 8px;
    overflow-x: auto; margin: 8px 0; font-size: 13px;
}
.msg-content code {
    background: #2a2a2a; padding: 1px 5px; border-radius: 4px;
    font-size: 13px; font-family: "SF Mono", Monaco, monospace;
}
.msg-content pre code { background: none; padding: 0; }
.msg-content p { margin: 6px 0; }
.msg-content ul, .msg-content ol { padding-left: 20px; margin: 6px 0; }
.msg-content a { color: var(--accent); text-decoration: none; }
.msg-content a:hover { text-decoration: underline; }
.msg-content strong { color: #fff; }

.thinking { display: flex; align-items: center; gap: 8px; color: var(--text-dim); font-size: 13px; padding: 4px 0; }
.thinking-dots span {
    width: 6px; height: 6px; border-radius: 50%; background: var(--text-dim);
    display: inline-block; animation: bounce .6s infinite alternate;
}
.thinking-dots span:nth-child(2) { animation-delay: .2s; }
.thinking-dots span:nth-child(3) { animation-delay: .4s; }
@keyframes bounce { to { opacity: .3; transform: translateY(-4px); } }

.input-area { padding: 16px 20px; border-top: 1px solid var(--border); background: var(--surface); }
.input-wrapper {
    display: flex; align-items: flex-end; gap: 8px;
    background: var(--surface2); border: 1px solid var(--border);
    border-radius: var(--radius); padding: 8px 12px; transition: border-color .15s;
}
.input-wrapper:focus-within { border-color: var(--accent); }
#messageInput {
    flex: 1; border: none; outline: none; background: transparent;
    color: var(--text); font-size: 14px; resize: none;
    line-height: 1.5; max-height: 120px; font-family: inherit;
}
#messageInput::placeholder { color: var(--text-dim); }
.send-btn {
    width: 36px; height: 36px; border: none; border-radius: 8px;
    background: var(--accent); color: #fff; cursor: pointer;
    display: flex; align-items: center; justify-content: center;
    transition: all .15s; flex-shrink: 0;
}
.send-btn:hover { background: var(--accent-hover); }
.send-btn:disabled { opacity: .4; cursor: not-allowed; }

.chat-container::-webkit-scrollbar { width: 6px; }
.chat-container::-webkit-scrollbar-track { background: transparent; }
.chat-container::-webkit-scrollbar-thumb { background: var(--border); border-radius: 3px; }

@media (max-width: 600px) {
    .msg-content { max-width: 88%; }
    .header h1 { font-size: 16px; }
    .quick-actions { flex-direction: column; align-items: center; }
}
`
}
