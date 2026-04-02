#!/usr/bin/env python3
import sys

path = '/Users/bytedance/go/src/agent/cmd/agent/main.go'
with open(path, 'r') as f:
    content = f.read()

# 2. Add ctxWindow var to chatCmd (first occurrence)
old = "\t\tbudget     int64\n\t)"
new = "\t\tbudget     int64\n\t\tctxWindow  int\n\t)"
content = content.replace(old, new, 1)

# 3. Add --ctx-window flag parsing in chatCmd
old = '\t\tcase a == "--memory":\n\t\t\tmemMode = true'
new = '''\t\tcase a == "--ctx-window":
\t\t\tif i+1 < len(args) {
\t\t\t\ti++
\t\t\t\tif v, err := strconv.Atoi(args[i]); err == nil {
\t\t\t\t\tctxWindow = v
\t\t\t\t}
\t\t\t}
\t\tcase a == "--memory":
\t\t\tmemMode = true'''
content = content.replace(old, new, 1)

# 4. Add context window manager setup in chatCmd
old = "\tloopAgent := agent.NewLoopAgent(llmClient, reg, systemPrompt, traceW, memStore, compressor)\n\n\t// \u8bbe\u7f6e token \u7528\u91cf\u8ffd\u8e2a"
new = """\tloopAgent := agent.NewLoopAgent(llmClient, reg, systemPrompt, traceW, memStore, compressor)

\t// \u8bbe\u7f6e\u4e0a\u4e0b\u6587\u7a97\u53e3\u7ba1\u7406
\t{
\t\tvar modelName string
\t\tif oai, ok := llmClient.(*llm.OpenAICompatClient); ok {
\t\t\tmodelName = oai.Model
\t\t}
\t\tprofile := ctxwindow.LookupModel(modelName)
\t\tif ctxWindow > 0 {
\t\t\tprofile.MaxContextTokens = ctxWindow
\t\t}
\t\tctxMgr := ctxwindow.NewManager(ctxwindow.ManagerConfig{
\t\t\tModel:               profile,
\t\t\tProtectRecentRounds: 2,
\t\t\tToolResultMaxTokens: 2000,
\t\t})
\t\tloopAgent.SetContextManager(ctxMgr)
\t\t_, _ = fmt.Fprintf(os.Stderr, "\xf0\x9f\x93\x90 \u4e0a\u4e0b\u6587\u7a97\u53e3: %d tokens (\u6a21\u578b: %s, \u8f93\u5165\u9884\u7b97: %d)\\n",
\t\t\tprofile.MaxContextTokens, profile.Name, ctxMgr.Config().MaxInputTokens)
\t}

\t// \u8bbe\u7f6e token \u7528\u91cf\u8ffd\u8e2a"""
content = content.replace(old, new, 1)

with open(path, 'w') as f:
    f.write(content)

print("patched successfully")
