// SPDX-License-Identifier: GPL-3.0-only
package app

import "net/http"

// One inventory owns dispatch and the settings page's endpoint list.
type apiRoute struct {
	method, path, description string
	handle                    func(*App, http.ResponseWriter, *http.Request)
}

var apiRoutes = []apiRoute{
	{"GET", "/api/v1/health", "健康检查", (*App).apiHandleHealth},
	{"POST", "/api/v1/ai/complete", "直接调用模型补全", (*App).apiHandleAIComplete},
	{"GET", "/api/v1/sessions", "会话列表", (*App).apiHandleListSessions},
	{"POST", "/api/v1/sessions", "创建会话", (*App).apiHandleCreateSession},
	{"GET", "/api/v1/sessions/{id}", "会话状态", (*App).apiHandleSessionStatus},
	{"GET", "/api/v1/sessions/{id}/result", "任务结果", (*App).apiHandleSessionResult},
	{"GET", "/api/v1/sessions/{id}/messages", "读取会话消息", (*App).apiHandleSessionMessages},
	{"GET", "/api/v1/sessions/{id}/todos", "会话计划", (*App).apiHandleSessionPlan},
	{"GET", "/api/v1/sessions/{id}/questions", "待回答问题", (*App).apiHandleSessionQuestions},
	{"POST", "/api/v1/sessions/{id}/answers", "回答问题", (*App).apiHandleSessionAnswers},
	{"POST", "/api/v1/sessions/{id}/messages", "发送消息", (*App).apiHandleSendMessage},
	{"POST", "/api/v1/sessions/{id}/cancel", "取消运行", (*App).apiHandleCancelSession},
	{"POST", "/api/v1/sessions/{id}/compact", "压缩上下文", (*App).apiHandleCompactSession},
	{"POST", "/api/v1/sessions/{id}/compact/fast", "快速压缩", (*App).apiHandleFastCompactSession},
	{"GET", "/api/v1/sessions/{id}/context", "上下文用量", (*App).apiHandleSessionContext},
	{"DELETE", "/api/v1/sessions/{id}", "删除会话", (*App).apiHandleDeleteSession},
	{"GET", "/api/v1/models", "模型列表", (*App).apiHandleListModels},
	{"POST", "/api/v1/models", "保存模型", (*App).apiHandleSaveModel},
	{"POST", "/api/v1/models/activate", "切换模型", (*App).apiHandleActivateModel},
	{"DELETE", "/api/v1/models/{index}", "删除模型", (*App).apiHandleDeleteModel},
	{"GET", "/api/v1/mcp", "MCP 状态", (*App).apiHandleGetMcp},
	{"PUT", "/api/v1/mcp/config", "保存 MCP 配置", (*App).apiHandlePutMcpConfig},
	{"POST", "/api/v1/mcp/reconnect", "重新连接", (*App).apiHandleReconnectMcp},
	{"GET", "/api/v1/skills", "技能列表", (*App).apiHandleListSkills},
	{"POST", "/api/v1/skills/refresh", "刷新", (*App).apiHandleRefreshSkills},
	{"GET", "/api/v1/skills/{name}", "技能内容", (*App).apiHandleGetSkill},
	{"POST", "/api/v1/skills/{name}/enable", "启用技能", (*App).apiHandleEnableSkill},
	{"POST", "/api/v1/skills/{name}/disable", "停用技能", (*App).apiHandleDisableSkill},
	{"GET", "/api/v1/tools", "工具清单", (*App).apiHandleListTools},
	{"GET", "/api/v1/subagents", "子代理运行记录", (*App).apiHandleListSubagents},
	{"GET", "/api/v1/workspace", "工作区信息", (*App).apiHandleGetWorkspace},
	{"GET", "/api/v1/context/settings", "上下文加载", (*App).apiHandleContextSettings},
	{"PUT", "/api/v1/context/settings", "保存设置", (*App).apiHandlePutContextSettings},
	{"GET", "/api/v1/stats/tokens", "Token 统计", (*App).apiHandleTokenStats},
	{"GET", "/api/v1/ssh/servers", "SSH 服务器", (*App).apiHandleListSSHServers},
	{"POST", "/api/v1/ssh/servers", "保存服务器", (*App).apiHandleSaveSSHServer},
	{"DELETE", "/api/v1/ssh/servers/{id}", "删除服务器", (*App).apiHandleDeleteSSHServer},
	{"GET", "/api/v1/ssh/authorizations", "工作区授权", (*App).apiHandleGetSSHAuthorizations},
	{"PUT", "/api/v1/ssh/authorizations", "工作区授权", (*App).apiHandlePutSSHAuthorizations},
	{"GET", "/api/v1/services", "后台服务", (*App).apiHandleListServices},
	{"POST", "/api/v1/services", "启动服务", (*App).apiHandleStartService},
	{"GET", "/api/v1/services/{id}", "服务详情", (*App).apiHandleGetService},
	{"POST", "/api/v1/services/{id}/restart", "重启服务", (*App).apiHandleRestartService},
	{"GET", "/api/v1/services/{id}/output", "服务输出", (*App).apiHandleServiceOutput},
	{"POST", "/api/v1/services/{id}/stop", "停止服务", (*App).apiHandleStopService},
	{"GET", "/api/v1/tasks", "定时任务", (*App).apiHandleListTasks},
	{"POST", "/api/v1/tasks", "创建任务", (*App).apiHandleCreateTask},
	{"GET", "/api/v1/tasks/{id}", "任务详情", (*App).apiHandleGetTask},
	{"DELETE", "/api/v1/tasks/{id}", "删除任务", (*App).apiHandleDeleteTask},
}

// APIEndpoints returns a copy containing only public route metadata.
func APIEndpoints() [][3]string {
	out := make([][3]string, 0, len(apiRoutes))
	for _, route := range apiRoutes {
		out = append(out, [3]string{route.method, route.path, route.description})
	}
	return out
}
