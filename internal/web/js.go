package web

func buildAppJS() string {
	// 注意: JS 中不使用反引号模板字符串，避免 Go raw string 冲突
	return `(function() {
    var chatContainer = document.getElementById('chatContainer');
    var messageInput = document.getElementById('messageInput');
    var sendBtn = document.getElementById('sendBtn');
    var clearBtn = document.getElementById('clearBtn');
    var statusBadge = document.getElementById('statusBadge');
    var toggleStats = document.getElementById('toggleStats');
    var contextBar = document.getElementById('contextBar');

    var sessionId = '';
    var isStreaming = false;
    var statsVisible = true;

    // 工具调用状态追踪
    var activeToolCards = {};

    checkStatus();
    messageInput.focus();

    sendBtn.addEventListener('click', sendMessage);
    clearBtn.addEventListener('click', clearChat);
    toggleStats.addEventListener('click', function() {
        statsVisible = !statsVisible;
        contextBar.style.display = statsVisible ? 'flex' : 'none';
        toggleStats.style.opacity = statsVisible ? '1' : '0.5';
    });
    messageInput.addEventListener('keydown', function(e) {
        if (e.key === 'Enter' && !e.shiftKey) {
            e.preventDefault();
            sendMessage();
        }
    });
    messageInput.addEventListener('input', autoResize);

    // ---- 侧边栏与历史对话 ----
    var sidebar = document.getElementById('sidebar');
    var sidebarToggle = document.getElementById('sidebarToggle');
    var sidebarClose = document.getElementById('sidebarClose');
    var newChatBtn = document.getElementById('newChatBtn');
    var sessionListEl = document.getElementById('sessionList');

    // 从 localStorage 读取侧边栏状态
    var sidebarState = localStorage.getItem('sidebar_state');
    if (sidebarState === 'collapsed') {
        sidebar.classList.add('collapsed');
    }

    sidebarToggle.addEventListener('click', function() {
        sidebar.classList.toggle('collapsed');
        localStorage.setItem('sidebar_state', sidebar.classList.contains('collapsed') ? 'collapsed' : 'expanded');
    });
    sidebarClose.addEventListener('click', function() {
        sidebar.classList.add('collapsed');
        localStorage.setItem('sidebar_state', 'collapsed');
    });
    newChatBtn.addEventListener('click', function() {
        startNewChat();
    });

    function startNewChat() {
        sessionId = '';
        activeToolCards = {};
        chatContainer.innerHTML =
            '<div class="welcome-msg">' +
            '<div class="welcome-icon">&#x1f916;</div>' +
            '<h2>Agent Chat</h2>' +
            '<p>Ready to assist!</p>' +
            '<div class="quick-actions">' +
            '<button class="quick-btn" onclick="sendQuick(\x27列出当前目录\x27)">&#x1f4c2; 列出目录</button>' +
            '<button class="quick-btn" onclick="sendQuick(\x27计算 (123+456)*789\x27)">&#x1f9ee; 计算</button>' +
            '</div></div>';
        updateContextInfo({
            max_input_tokens: 0, estimated_tokens: 0, usage_percent: 0,
            remaining_tokens: 0, message_count: 0, has_room: true,
            total_tokens: 0, prompt_tokens: 0, completion_tokens: 0,
            call_count: 0, budget: 0, budget_remaining: 0
        });
        refreshSessionList();
        messageInput.focus();
    }

    function refreshSessionList() {
        fetch('/api/sessions').then(function(r) { return r.json(); }).then(function(sessions) {
            if (!sessions || sessions.length === 0) {
                sessionListEl.innerHTML = '<div class="session-empty">暂无历史对话</div>';
                return;
            }
            var html = '';
            for (var i = 0; i < sessions.length; i++) {
                var s = sessions[i];
                var isActive = s.id === sessionId;
                var timeStr = formatTimeAgo(s.updated_at);
                html += '<div class="session-item' + (isActive ? ' active' : '') + '" data-sid="' + escapeHtml(s.id) + '">' +
                    '<div class="session-item-content">' +
                    '<div class="session-item-title">' + escapeHtml(s.title) + '</div>' +
                    '<div class="session-item-meta"><span>' + s.msg_count + ' 条消息</span><span>' + timeStr + '</span></div>' +
                    '</div>' +
                    '<div class="session-item-actions">' +
                    '<button class="session-action-btn rename" title="重命名" data-sid="' + escapeHtml(s.id) + '">&#x270f;&#xfe0f;</button>' +
                    '<button class="session-action-btn delete" title="删除" data-sid="' + escapeHtml(s.id) + '">&#x1f5d1;&#xfe0f;</button>' +
                    '</div></div>';
            }
            sessionListEl.innerHTML = html;

            var items = sessionListEl.querySelectorAll('.session-item');
            for (var j = 0; j < items.length; j++) {
                (function(item) {
                    item.addEventListener('click', function(e) {
                        if (e.target.closest('.session-action-btn')) return;
                        var sid = item.getAttribute('data-sid');
                        switchToSession(sid);
                    });
                })(items[j]);
            }

            var renameBtns = sessionListEl.querySelectorAll('.session-action-btn.rename');
            for (var k = 0; k < renameBtns.length; k++) {
                (function(btn) {
                    btn.addEventListener('click', function(e) {
                        e.stopPropagation();
                        var sid = btn.getAttribute('data-sid');
                        var newTitle = prompt('输入新标题:');
                        if (newTitle && newTitle.trim()) {
                            fetch('/api/sessions/rename', {
                                method: 'POST',
                                headers: { 'Content-Type': 'application/json' },
                                body: JSON.stringify({ session_id: sid, title: newTitle.trim() })
                            }).then(function() { refreshSessionList(); });
                        }
                    });
                })(renameBtns[k]);
            }

            var delBtns = sessionListEl.querySelectorAll('.session-action-btn.delete');
            for (var m = 0; m < delBtns.length; m++) {
                (function(btn) {
                    btn.addEventListener('click', function(e) {
                        e.stopPropagation();
                        var sid = btn.getAttribute('data-sid');
                        if (confirm('确定删除这个对话？')) {
                            fetch('/api/sessions/delete', {
                                method: 'POST',
                                headers: { 'Content-Type': 'application/json' },
                                body: JSON.stringify({ session_id: sid })
                            }).then(function() {
                                if (sid === sessionId) {
                                    startNewChat();
                                } else {
                                    refreshSessionList();
                                }
                            });
                        }
                    });
                })(delBtns[m]);
            }
        }).catch(function(e) {
            sessionListEl.innerHTML = '<div class="session-empty">加载失败</div>';
        });
    }

    function switchToSession(sid) {
        if (sid === sessionId) return;
        if (isStreaming) return;
        sessionId = sid;
        chatContainer.innerHTML = '<div class="message agent"><div class="msg-avatar">&#x1f916;</div><div class="msg-content"><div class="thinking"><div class="thinking-dots"><span></span><span></span><span></span></div> 加载对话...</div></div></div>';
        refreshSessionList();

        fetch('/api/context?session_id=' + encodeURIComponent(sid))
            .then(function(r) { return r.json(); })
            .then(function(data) { updateContextInfo(data); })
            .catch(function() {});

        setTimeout(function() {
            chatContainer.innerHTML = '';
            appendMessage('agent', '已切换到此对话。继续输入消息来继续对话。');
        }, 300);
    }

    function formatTimeAgo(ts) {
        if (!ts) return '';
        var now = Math.floor(Date.now() / 1000);
        var diff = now - ts;
        if (diff < 60) return '刚刚';
        if (diff < 3600) return Math.floor(diff / 60) + ' 分钟前';
        if (diff < 86400) return Math.floor(diff / 3600) + ' 小时前';
        if (diff < 604800) return Math.floor(diff / 86400) + ' 天前';
        var d = new Date(ts * 1000);
        return (d.getMonth() + 1) + '/' + d.getDate();
    }

    // 初始加载会话列表
    refreshSessionList();


    function autoResize() {
        messageInput.style.height = 'auto';
        messageInput.style.height = Math.min(messageInput.scrollHeight, 120) + 'px';
    }

    async function checkStatus() {
        try {
            var resp = await fetch('/api/status');
            await resp.json();
            statusBadge.textContent = 'online';
            statusBadge.className = 'status-badge ok';
        } catch (e) {
            statusBadge.textContent = 'offline';
            statusBadge.className = 'status-badge err';
        }
    }

    // ---- 上下文/Token 用量更新 ----

    function updateContextInfo(data) {
        if (!data) return;

        var percent = 0;
        if (data.max_input_tokens > 0) {
            percent = Math.min(data.usage_percent * 100, 100);
        }

        var fillEl = document.getElementById('ctxProgressFill');
        fillEl.style.width = percent.toFixed(1) + '%';

        if (percent < 50) {
            fillEl.className = 'ctx-progress-fill level-ok';
        } else if (percent < 75) {
            fillEl.className = 'ctx-progress-fill level-warn';
        } else if (percent < 90) {
            fillEl.className = 'ctx-progress-fill level-high';
        } else {
            fillEl.className = 'ctx-progress-fill level-critical';
        }

        document.getElementById('ctxRatio').textContent =
            formatNumber(data.estimated_tokens) + ' / ' + formatNumber(data.max_input_tokens) + ' tokens';
        document.getElementById('ctxPercent').textContent = percent.toFixed(1) + '%';
        document.getElementById('ctxMessages').textContent = data.message_count + ' 条消息';
        document.getElementById('ctxRemaining').textContent =
            '剩余 ' + formatNumber(data.remaining_tokens) + ' tokens';

        document.getElementById('tkTotal').textContent = formatNumber(data.total_tokens);
        document.getElementById('tkPrompt').textContent = formatNumber(data.prompt_tokens);
        document.getElementById('tkCompletion').textContent = formatNumber(data.completion_tokens);
        document.getElementById('tkCalls').textContent = data.call_count || 0;

        var budgetEl = document.getElementById('ctxBudget');
        if (data.budget > 0) {
            var budgetPct = (data.total_tokens / data.budget * 100).toFixed(1);
            budgetEl.textContent = '预算: ' + formatNumber(data.budget) + ' (' + budgetPct + '%)';
            budgetEl.style.display = 'inline';
        } else {
            budgetEl.style.display = 'none';
        }
    }

    function formatNumber(n) {
        if (n === undefined || n === null) return '0';
        n = Number(n);
        if (n >= 1000000) return (n / 1000000).toFixed(1) + 'M';
        if (n >= 1000) return (n / 1000).toFixed(1) + 'K';
        return String(n);
    }

    // ---- 工具调用卡片 ----

    function createToolCard(data) {
        var card = document.createElement('div');
        card.className = 'tool-card running';
        card.id = 'tool-' + data.tool_call_id;

        var header = document.createElement('div');
        header.className = 'tool-card-header';
        header.innerHTML =
            '<span class="tool-icon">⚙️</span>' +
            '<span class="tool-name">' + escapeHtml(data.tool_name) + '</span>' +
            '<span class="tool-status-badge running">运行中...</span>';

        var body = document.createElement('div');
        body.className = 'tool-card-body';
        body.style.display = 'none';

        // 参数预览
        if (data.tool_args && data.tool_args !== '{}') {
            var argsDiv = document.createElement('div');
            argsDiv.className = 'tool-args';
            try {
                var parsed = JSON.parse(data.tool_args);
                var keys = Object.keys(parsed);
                var preview = keys.slice(0, 3).map(function(k) {
                    var v = String(parsed[k]);
                    if (v.length > 60) v = v.substring(0, 60) + '...';
                    return '<span class="tool-arg-key">' + escapeHtml(k) + '</span>: ' + escapeHtml(v);
                }).join('<br>');
                if (keys.length > 3) preview += '<br><span class="tool-arg-more">+' + (keys.length - 3) + ' more</span>';
                argsDiv.innerHTML = '<div class="tool-section-label">参数</div>' + preview;
            } catch(e) {
                argsDiv.innerHTML = '<div class="tool-section-label">参数</div>' + escapeHtml(data.tool_args);
            }
            body.appendChild(argsDiv);
        }

        // 结果区（待填充）
        var resultDiv = document.createElement('div');
        resultDiv.className = 'tool-result';
        resultDiv.style.display = 'none';
        body.appendChild(resultDiv);

        // 点击展开/折叠
        header.addEventListener('click', function() {
            if (body.style.display === 'none') {
                body.style.display = 'block';
            } else {
                body.style.display = 'none';
            }
        });

        card.appendChild(header);
        card.appendChild(body);
        return card;
    }

    function updateToolCard(data) {
        var card = document.getElementById('tool-' + data.tool_call_id);
        if (!card) return;

        card.className = data.tool_error ? 'tool-card error' : 'tool-card done';

        // 更新状态
        var badge = card.querySelector('.tool-status-badge');
        if (badge) {
            if (data.tool_error) {
                badge.className = 'tool-status-badge error';
                badge.textContent = '失败';
            } else {
                badge.className = 'tool-status-badge done';
                badge.textContent = data.duration + 'ms';
            }
        }

        // 填充结果
        var resultDiv = card.querySelector('.tool-result');
        if (resultDiv && data.tool_result) {
            resultDiv.style.display = 'block';
            var label = data.tool_error ? '错误' : '结果';
            resultDiv.innerHTML = '<div class="tool-section-label">' + label + '</div>' +
                '<pre class="tool-result-pre">' + escapeHtml(data.tool_result) + '</pre>';
        }
    }

    // ---- 消息发送 ----

    window.sendQuick = function(text) {
        messageInput.value = text;
        sendMessage();
    };

    async function sendMessage() {
        var text = messageInput.value.trim();
        if (!text || isStreaming) return;

        var welcome = chatContainer.querySelector('.welcome-msg');
        if (welcome) welcome.remove();

        appendMessage('user', text);
        messageInput.value = '';
        messageInput.style.height = 'auto';
        isStreaming = true;
        sendBtn.disabled = true;
        activeToolCards = {};

        var thinkingEl = appendThinking();

        try {
            await streamChat(text, thinkingEl);
        } catch (e) {
            appendMessage('agent', 'Error: ' + e.message);
        } finally {
            if (thinkingEl && thinkingEl.parentNode) thinkingEl.remove();
        }

        isStreaming = false;
        sendBtn.disabled = false;
        messageInput.focus();
    }

    async function streamChat(text, thinkingEl) {
        var resp = await fetch('/api/chat/stream', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ message: text, session_id: sessionId })
        });

        if (!resp.ok) throw new Error('Server error: ' + resp.status);

        var reader = resp.body.getReader();
        var decoder = new TextDecoder();
        var buffer = '';
        var agentContent = '';
        var agentEl = null;
        var toolsContainer = null;

        while (true) {
            var chunk = await reader.read();
            if (chunk.done) break;

            buffer += decoder.decode(chunk.value, { stream: true });
            var sseLines = buffer.split('\n');
            buffer = sseLines.pop() || '';

            var eventType = '';
            var dataLines = [];

            for (var i = 0; i < sseLines.length; i++) {
                var line = sseLines[i];
                if (line.indexOf('event: ') === 0) {
                    if (eventType && dataLines.length > 0) {
                        handleEvent(eventType, dataLines.join('\n'));
                        dataLines = [];
                    }
                    eventType = line.slice(7);
                } else if (line.indexOf('data: ') === 0) {
                    dataLines.push(line.slice(6));
                } else if (line === '' && eventType) {
                    handleEvent(eventType, dataLines.join('\n'));
                    eventType = '';
                    dataLines = [];
                }
            }
            if (eventType && dataLines.length > 0) {
                handleEvent(eventType, dataLines.join('\n'));
            }
        }

        // 处理流结束后 buffer 中的残余数据
        if (buffer.trim()) {
            var remainLines = buffer.split('\n');
            var lastEvent = '';
            var lastData = [];
            for (var j = 0; j < remainLines.length; j++) {
                var rl = remainLines[j];
                if (rl.indexOf('event: ') === 0) {
                    lastEvent = rl.slice(7);
                } else if (rl.indexOf('data: ') === 0) {
                    lastData.push(rl.slice(6));
                }
            }
            if (lastEvent && lastData.length > 0) {
                handleEvent(lastEvent, lastData.join('\n'));
            }
        }

        function ensureToolsContainer() {
            if (!toolsContainer) {
                toolsContainer = document.createElement('div');
                toolsContainer.className = 'tools-container';
                var wrapper = document.createElement('div');
                wrapper.className = 'message agent';
                wrapper.innerHTML = '<div class="msg-avatar">🛠️</div>';
                wrapper.appendChild(toolsContainer);
                chatContainer.appendChild(wrapper);
            }
            return toolsContainer;
        }

        function handleEvent(event, data) {
            if (event === 'session') {
                sessionId = data;
            } else if (event === 'delta') {
                if (thinkingEl && thinkingEl.parentNode) thinkingEl.remove();
                // 如果之前有工具调用，开始新的 agent 文本消息
                if (!agentEl) {
                    agentEl = appendMessage('agent', '');
                    toolsContainer = null; // 新消息轮
                }
                agentContent += data;
                updateMessageContent(agentEl, agentContent);
                scrollToBottom();
            } else if (event === 'tool_start') {
                if (thinkingEl && thinkingEl.parentNode) thinkingEl.remove();
                try {
                    var toolData = JSON.parse(data);
                    var container = ensureToolsContainer();
                    var card = createToolCard(toolData);
                    container.appendChild(card);
                    activeToolCards[toolData.tool_call_id] = card;
                    scrollToBottom();
                } catch (e) {}
            } else if (event === 'tool_end') {
                try {
                    var toolData = JSON.parse(data);
                    updateToolCard(toolData);
                    scrollToBottom();
                } catch (e) {}
            } else if (event === 'iteration') {
                // 迭代事件：更新 thinking 提示
                try {
                    var iterData = JSON.parse(data);
                    if (thinkingEl && thinkingEl.parentNode) {
                        var thinkDiv = thinkingEl.querySelector('.thinking');
                        if (thinkDiv && iterData.iteration > 1) {
                            thinkDiv.innerHTML =
                                '<div class="thinking-dots"><span></span><span></span><span></span></div>' +
                                ' thinking... (轮次 ' + iterData.iteration + '/' + iterData.max_iter + ')';
                        }
                    }
                } catch (e) {}
            } else if (event === 'status') {
                // 状态事件：更新 thinking 文本
                if (thinkingEl && thinkingEl.parentNode) {
                    var thinkDiv = thinkingEl.querySelector('.thinking');
                    if (thinkDiv) {
                        var statusText = data === 'calling_tools' ? '调用工具中...' :
                                        data === 'waiting_llm' ? '等待模型响应...' : data;
                        thinkDiv.innerHTML =
                            '<div class="thinking-dots"><span></span><span></span><span></span></div>' +
                            ' ' + statusText;
                    }
                }
            } else if (event === 'thinking') {
                // 思考过程（reasoning model）
                if (thinkingEl && thinkingEl.parentNode) {
                    var thinkDiv = thinkingEl.querySelector('.thinking');
                    if (thinkDiv) {
                        thinkDiv.innerHTML =
                            '<div class="thinking-dots"><span></span><span></span><span></span></div>' +
                            ' <span class="thinking-text">' + escapeHtml(data).substring(0, 100) + '</span>';
                    }
                }
            } else if (event === 'context') {
                try {
                    var ctxData = JSON.parse(data);
                    updateContextInfo(ctxData);
                } catch (e) {}
            } else if (event === 'done') {
                if (thinkingEl && thinkingEl.parentNode) thinkingEl.remove();
                if (!agentEl && data) {
                    agentEl = appendMessage('agent', data);
                } else if (agentEl && !agentContent && data) {
                    updateMessageContent(agentEl, data);
                }
                // 重置工具状态
                activeToolCards = {};
                agentEl = null;
                agentContent = '';
                toolsContainer = null;
                // 更新侧边栏会话列表
                refreshSessionList();
            } else if (event === 'error') {
                if (thinkingEl && thinkingEl.parentNode) thinkingEl.remove();
                appendMessage('agent', 'Error: ' + data);
            }
        }
    }

    function appendMessage(role, content) {
        var msgDiv = document.createElement('div');
        msgDiv.className = 'message ' + role;

        var avatar = document.createElement('div');
        avatar.className = 'msg-avatar';
        avatar.textContent = role === 'user' ? '👤' : '🤖';

        var contentDiv = document.createElement('div');
        contentDiv.className = 'msg-content';
        updateMessageContent(contentDiv, content);

        msgDiv.appendChild(avatar);
        msgDiv.appendChild(contentDiv);
        chatContainer.appendChild(msgDiv);
        scrollToBottom();
        return contentDiv;
    }

    function updateMessageContent(el, content) {
        el.innerHTML = renderMarkdown(content);
    }

    function appendThinking() {
        var div = document.createElement('div');
        div.className = 'message agent';
        div.innerHTML =
            '<div class="msg-avatar">🤖</div>' +
            '<div class="msg-content"><div class="thinking">' +
            '<div class="thinking-dots"><span></span><span></span><span></span></div>' +
            ' thinking...</div></div>';
        chatContainer.appendChild(div);
        scrollToBottom();
        return div;
    }

    function renderMarkdown(text) {
        if (!text) return '';
        var html = escapeHtml(text);
        // code blocks
        html = html.replace(/` + "`" + `{3}(\w*)\n([\s\S]*?)` + "`" + `{3}/g, function(match, lang, code) {
            return '<pre><code class="lang-' + lang + '">' + code + '</code></pre>';
        });
        html = html.replace(/` + "`" + `([^` + "`" + `]+)` + "`" + `/g, '<code>$1</code>');
        html = html.replace(/\*\*(.+?)\*\*/g, '<strong>$1</strong>');
        html = html.replace(/\*(.+?)\*/g, '<em>$1</em>');
        html = html.replace(/\[([^\]]+)\]\(([^)]+)\)/g, '<a href="$2" target="_blank">$1</a>');
        html = html.replace(/^### (.+)$/gm, '<h4>$1</h4>');
        html = html.replace(/^## (.+)$/gm, '<h3>$1</h3>');
        html = html.replace(/^# (.+)$/gm, '<h2>$1</h2>');
        html = html.replace(/\n{2,}/g, '</p><p>');
        html = html.replace(/\n/g, '<br>');
        html = '<p>' + html + '</p>';
        html = html.replace(/<p>\s*<\/p>/g, '');
        return html;
    }

    function escapeHtml(str) {
        return str.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
    }

    function scrollToBottom() {
        requestAnimationFrame(function() {
            chatContainer.scrollTop = chatContainer.scrollHeight;
        });
    }

    async function clearChat() {
        if (sessionId) {
            await fetch('/api/sessions/clear', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ session_id: sessionId })
            });
        }
        sessionId = '';
        activeToolCards = {};
        chatContainer.innerHTML =
            '<div class="welcome-msg">' +
            '<div class="welcome-icon">🤖</div>' +
            '<h2>Agent Chat</h2>' +
            '<p>Ready to assist!</p>' +
            '<div class="quick-actions">' +
            '<button class="quick-btn" onclick="sendQuick(\x27列出当前目录\x27)">📂 列出目录</button>' +
            '<button class="quick-btn" onclick="sendQuick(\x27计算 (123+456)*789\x27)">🧮 计算</button>' +
            '</div></div>';
        updateContextInfo({
            max_input_tokens: 0, estimated_tokens: 0, usage_percent: 0,
            remaining_tokens: 0, message_count: 0, has_room: true,
            total_tokens: 0, prompt_tokens: 0, completion_tokens: 0,
            call_count: 0, budget: 0, budget_remaining: 0
        });
        sessionId = '';
        refreshSessionList();
    }
})();`
}
