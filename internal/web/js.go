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
        document.getElementById('ctxMessages').textContent = data.message_count + ' \\u6761\\u6d88\\u606f';
        document.getElementById('ctxRemaining').textContent =
            '\\u5269\\u4f59 ' + formatNumber(data.remaining_tokens) + ' tokens';

        document.getElementById('tkTotal').textContent = formatNumber(data.total_tokens);
        document.getElementById('tkPrompt').textContent = formatNumber(data.prompt_tokens);
        document.getElementById('tkCompletion').textContent = formatNumber(data.completion_tokens);
        document.getElementById('tkCalls').textContent = data.call_count || 0;

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

    // ---- 工具调用卡片 ----

    function createToolCard(data) {
        var card = document.createElement('div');
        card.className = 'tool-card running';
        card.id = 'tool-' + data.tool_call_id;

        var header = document.createElement('div');
        header.className = 'tool-card-header';
        header.innerHTML =
            '<span class="tool-icon">\\u2699\\ufe0f</span>' +
            '<span class="tool-name">' + escapeHtml(data.tool_name) + '</span>' +
            '<span class="tool-status-badge running">\\u8fd0\\u884c\\u4e2d...</span>';

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
                argsDiv.innerHTML = '<div class="tool-section-label">\\u53c2\\u6570</div>' + preview;
            } catch(e) {
                argsDiv.innerHTML = '<div class="tool-section-label">\\u53c2\\u6570</div>' + escapeHtml(data.tool_args);
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
                badge.textContent = '\\u5931\\u8d25';
            } else {
                badge.className = 'tool-status-badge done';
                badge.textContent = data.duration + 'ms';
            }
        }

        // 填充结果
        var resultDiv = card.querySelector('.tool-result');
        if (resultDiv && data.tool_result) {
            resultDiv.style.display = 'block';
            var label = data.tool_error ? '\\u9519\\u8bef' : '\\u7ed3\\u679c';
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
        var toolsContainer = null;

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

        function ensureToolsContainer() {
            if (!toolsContainer) {
                toolsContainer = document.createElement('div');
                toolsContainer.className = 'tools-container';
                var wrapper = document.createElement('div');
                wrapper.className = 'message agent';
                wrapper.innerHTML = '<div class="msg-avatar">\\ud83d\\udee0\\ufe0f</div>';
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
                                ' thinking... (\\u8f6e\\u6b21 ' + iterData.iteration + '/' + iterData.max_iter + ')';
                        }
                    }
                } catch (e) {}
            } else if (event === 'status') {
                // 状态事件：更新 thinking 文本
                if (thinkingEl && thinkingEl.parentNode) {
                    var thinkDiv = thinkingEl.querySelector('.thinking');
                    if (thinkDiv) {
                        var statusText = data === 'calling_tools' ? '\\u8c03\\u7528\\u5de5\\u5177\\u4e2d...' :
                                        data === 'waiting_llm' ? '\\u7b49\\u5f85\\u6a21\\u578b\\u54cd\\u5e94...' : data;
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
                if (!agentEl && data) agentEl = appendMessage('agent', data);
                // 重置工具状态
                activeToolCards = {};
                agentEl = null;
                agentContent = '';
                toolsContainer = null;
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
        // code blocks
        html = html.replace(/` + "`" + `{3}(\\w*)\\n([\\s\\S]*?)` + "`" + `{3}/g, function(match, lang, code) {
            return '<pre><code class="lang-' + lang + '">' + code + '</code></pre>';
        });
        html = html.replace(/` + "`" + `([^` + "`" + `]+)` + "`" + `/g, '<code>$1</code>');
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
        activeToolCards = {};
        chatContainer.innerHTML =
            '<div class="welcome-msg">' +
            '<div class="welcome-icon">\\ud83e\\udd16</div>' +
            '<h2>Agent Chat</h2>' +
            '<p>Ready to assist!</p>' +
            '<div class="quick-actions">' +
            "<button class=\\"quick-btn\\" onclick=\\"sendQuick('\\u5217\\u51fa\\u5f53\\u524d\\u76ee\\u5f55')\\">\\ud83d\\udcc2 \\u5217\\u51fa\\u76ee\\u5f55</button>" +
            "<button class=\\"quick-btn\\" onclick=\\"sendQuick('\\u8ba1\\u7b97 (123+456)*789')\\">\\ud83e\\uddee \\u8ba1\\u7b97</button>" +
            '</div></div>';
        updateContextInfo({
            max_input_tokens: 0, estimated_tokens: 0, usage_percent: 0,
            remaining_tokens: 0, message_count: 0, has_room: true,
            total_tokens: 0, prompt_tokens: 0, completion_tokens: 0,
            call_count: 0, budget: 0, budget_remaining: 0
        });
    }
})();`
}
