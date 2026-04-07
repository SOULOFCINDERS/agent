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

        // 上下文窗口进度条
        var percent = 0;
        if (data.max_input_tokens > 0) {
            percent = Math.min(data.usage_percent * 100, 100);
        }

        var fillEl = document.getElementById('ctxProgressFill');
        fillEl.style.width = percent.toFixed(1) + '%';

        // 进度条颜色：绿→黄→橙→红
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
        document.getElementById('ctxMessages').textContent = data.message_count + ' \\u6761\\u6d88\\u606f';
        document.getElementById('ctxRemaining').textContent =
            '\\u5269\\u4f59 ' + formatNumber(data.remaining_tokens) + ' tokens';

        // Token 用量
        document.getElementById('tkTotal').textContent = formatNumber(data.total_tokens);
        document.getElementById('tkPrompt').textContent = formatNumber(data.prompt_tokens);
        document.getElementById('tkCompletion').textContent = formatNumber(data.completion_tokens);
        document.getElementById('tkCalls').textContent = data.call_count || 0;

        // 预算
        var budgetEl = document.getElementById('ctxBudget');
        if (data.budget > 0) {
            var budgetPct = (data.total_tokens / data.budget * 100).toFixed(1);
            budgetEl.textContent = '\\u9884\\u7b97: ' + formatNumber(data.budget) + ' (' + budgetPct + '%)';
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

    async function fetchContextInfo() {
        if (!sessionId) return;
        try {
            var resp = await fetch('/api/context?session_id=' + encodeURIComponent(sessionId));
            var data = await resp.json();
            updateContextInfo(data);
        } catch (e) {
            // 静默失败
        }
    }

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

        var thinkingEl = appendThinking();

        try {
            await streamChat(text, thinkingEl);
        } catch (e) {
            if (thinkingEl.parentNode) thinkingEl.remove();
            appendMessage('agent', 'Error: ' + e.message);
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

        while (true) {
            var chunk = await reader.read();
            if (chunk.done) break;

            buffer += decoder.decode(chunk.value, { stream: true });
            var lines = buffer.split('\\n');
            buffer = lines.pop() || '';

            var eventType = '';
            var dataLines = [];

            for (var i = 0; i < lines.length; i++) {
                var line = lines[i];
                if (line.indexOf('event: ') === 0) {
                    if (eventType && dataLines.length > 0) {
                        handleEvent(eventType, dataLines.join('\\n'));
                        dataLines = [];
                    }
                    eventType = line.slice(7);
                } else if (line.indexOf('data: ') === 0) {
                    dataLines.push(line.slice(6));
                } else if (line === '' && eventType) {
                    handleEvent(eventType, dataLines.join('\\n'));
                    eventType = '';
                    dataLines = [];
                }
            }
            if (eventType && dataLines.length > 0) {
                handleEvent(eventType, dataLines.join('\\n'));
            }
        }

        function handleEvent(event, data) {
            if (event === 'session') {
                sessionId = data;
            } else if (event === 'delta') {
                if (thinkingEl && thinkingEl.parentNode) thinkingEl.remove();
                if (!agentEl) agentEl = appendMessage('agent', '');
                agentContent += data;
                updateMessageContent(agentEl, agentContent);
                scrollToBottom();
            } else if (event === 'context') {
                // 收到上下文信息，解析并更新 UI
                try {
                    var ctxData = JSON.parse(data);
                    updateContextInfo(ctxData);
                } catch (e) {}
            } else if (event === 'done') {
                if (thinkingEl && thinkingEl.parentNode) thinkingEl.remove();
                if (!agentEl && data) agentEl = appendMessage('agent', data);
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
        avatar.textContent = role === 'user' ? '\\ud83d\\udc64' : '\\ud83e\\udd16';

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
            '<div class="msg-avatar">\\ud83e\\udd16</div>' +
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
        html = html.replace(/\\*\\*(.+?)\\*\\*/g, '<strong>$1</strong>');
        html = html.replace(/\\*(.+?)\\*/g, '<em>$1</em>');
        html = html.replace(/\\[([^\\]]+)\\]\\(([^)]+)\\)/g, '<a href="$2" target="_blank">$1</a>');
        html = html.replace(/^### (.+)$/gm, '<h4>$1</h4>');
        html = html.replace(/^## (.+)$/gm, '<h3>$1</h3>');
        html = html.replace(/^# (.+)$/gm, '<h2>$1</h2>');
        html = html.replace(/\\n{2,}/g, '</p><p>');
        html = html.replace(/\\n/g, '<br>');
        html = '<p>' + html + '</p>';
        html = html.replace(/<p>\\s*<\\/p>/g, '');
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
        chatContainer.innerHTML =
            '<div class="welcome-msg">' +
            '<div class="welcome-icon">\\ud83e\\udd16</div>' +
            '<h2>Agent Chat</h2>' +
            '<p>Ready to assist!</p>' +
            '<div class="quick-actions">' +
            "<button class=\\"quick-btn\\" onclick=\\"sendQuick('\\u5217\\u51fa\\u5f53\\u524d\\u76ee\\u5f55')\\">\\ud83d\\udcc2 \\u5217\\u51fa\\u76ee\\u5f55</button>" +
            "<button class=\\"quick-btn\\" onclick=\\"sendQuick('\\u8ba1\\u7b97 (123+456)*789')\\">\\ud83e\\uddee \\u8ba1\\u7b97</button>" +
            '</div></div>';
        // 重置上下文面板
        updateContextInfo({
            max_input_tokens: 0, estimated_tokens: 0, usage_percent: 0,
            remaining_tokens: 0, message_count: 0, has_room: true,
            total_tokens: 0, prompt_tokens: 0, completion_tokens: 0,
            call_count: 0, budget: 0, budget_remaining: 0
        });
    }
})();`
}
