package web

func buildAppJS() string {
	// 注意: JS 中不使用反引号模板字符串，避免 Go raw string 冲突
	return `(function() {
    var chatContainer = document.getElementById('chatContainer');
    var messageInput = document.getElementById('messageInput');
    var sendBtn = document.getElementById('sendBtn');
    var clearBtn = document.getElementById('clearBtn');
    var statusBadge = document.getElementById('statusBadge');

    var sessionId = '';
    var isStreaming = false;

    checkStatus();
    messageInput.focus();

    sendBtn.addEventListener('click', sendMessage);
    clearBtn.addEventListener('click', clearChat);
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
            var lines = buffer.split('\n');
            buffer = lines.pop() || '';

            var eventType = '';
            var dataLines = [];

            for (var i = 0; i < lines.length; i++) {
                var line = lines[i];
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

        function handleEvent(event, data) {
            if (event === 'session') {
                sessionId = data;
            } else if (event === 'delta') {
                if (thinkingEl && thinkingEl.parentNode) thinkingEl.remove();
                if (!agentEl) agentEl = appendMessage('agent', '');
                agentContent += data;
                updateMessageContent(agentEl, agentContent);
                scrollToBottom();
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
        avatar.textContent = role === 'user' ? '\ud83d\udc64' : '\ud83e\udd16';

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
            '<div class="msg-avatar">\ud83e\udd16</div>' +
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
        chatContainer.innerHTML =
            '<div class="welcome-msg">' +
            '<div class="welcome-icon">\ud83e\udd16</div>' +
            '<h2>Agent Chat</h2>' +
            '<p>Ready to assist!</p>' +
            '<div class="quick-actions">' +
            "<button class=\"quick-btn\" onclick=\"sendQuick('\u5217\u51fa\u5f53\u524d\u76ee\u5f55')\">\ud83d\udcc2 \u5217\u51fa\u76ee\u5f55</button>" +
            "<button class=\"quick-btn\" onclick=\"sendQuick('\u8ba1\u7b97 (123+456)*789')\">\ud83e\uddee \u8ba1\u7b97</button>" +
            '</div></div>';
    }
})();`
}
