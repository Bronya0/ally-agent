// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// SSHClusterRequest is the argument shape for the ssh_cluster tool.
type SSHClusterRequest struct {
	Action      string `json:"action"`
	Alias       string `json:"alias,omitempty"`
	Host        string `json:"host,omitempty"`
	Port        int    `json:"port,omitempty"`
	Username    string `json:"username,omitempty"`
	Description string `json:"description,omitempty"`
	Reason      string `json:"reason,omitempty"`
}

// executeSSHClusterTool handles the ssh_cluster tool calls from the model.
// action="list" returns servers authorized for the workspace.
// action="add" requests approval from the user (Approval Gate 1) to register a new server.
func (a *App) executeSSHClusterTool(ctx context.Context, sessionID, workspace string, req SSHClusterRequest) (map[string]any, error) {
	action := strings.ToLower(strings.TrimSpace(req.Action))
	if action == "" {
		action = "list"
	}

	switch action {
	case "list":
		servers := a.ListAuthorizedSSHServers(workspace)
		return map[string]any{
			"ok":        true,
			"workspace": workspace,
			"count":     len(servers),
			"servers":   servers,
		}, nil

	case "add":
		alias := strings.ToLower(strings.TrimSpace(req.Alias))
		if alias == "" {
			return nil, codedToolError("E_BAD_SSH_CLUSTER", errors.New("alias is required for action=add"))
		}
		// 已登记的别名绝不覆盖：节点是用户配置的凭据（密码/私钥口令）、风险级与
		// 身份字段的唯一来源，而模型只会带 host/username/description（凭据字段恒空），
		// 走一次 upsert 就把用户存好的密码静默抹掉。把 add 当作“申请访问该节点”处理。
		if existing, ok := a.GetSSHServer(alias); ok {
			return a.authorizeExistingSSHServer(ctx, sessionID, workspace, existing)
		}
		host := strings.TrimSpace(req.Host)
		username := strings.TrimSpace(req.Username)
		description := strings.TrimSpace(req.Description)
		reason := strings.TrimSpace(req.Reason)
		// All four are needed for a node that is not registered yet. Report every
		// missing one at once: the schema can only require `alias` (the
		// already-registered path ignores the rest, so making them globally required
		// would force the model to invent values for an existing node), and failing
		// one field per round cost up to four wasted model turns.
		missing := make([]string, 0, 4)
		for _, field := range []struct{ name, value string }{
			{"host", host},
			{"username", username},
			{"description", description},
			{"reason", reason},
		} {
			if field.value == "" {
				missing = append(missing, field.name)
			}
		}
		if len(missing) > 0 {
			return nil, codedToolError("E_BAD_SSH_CLUSTER", fmt.Errorf("action=add needs %s when registering the new alias %q; send them together in one call, or use action=list to check whether the alias is already registered",
				strings.Join(missing, ", "), alias))
		}
		port := req.Port
		if port <= 0 {
			port = 22
		}

		// Approval Gate 1: When sessionID is present (chat interaction), prompt the user for approval.
		if sessionID != "" {
			askReq := AskRequest{
				Questions: []AskQuestion{
					{
						ID: "approve_cluster_add",
						Question: fmt.Sprintf("模型申请将新服务器节点登记到集群：\n- 别名: %s\n- 用途描述: %s\n- 主机: %s@%s:%d\n- 申请理由: %s\n\n是否批准登记并加入当前工作区白名单？",
							alias, description, username, host, port, reason),
						Options: []AskOption{
							{
								ID:          "approve",
								Label:       "批准登记并授权当前工作区",
								Description: "将该节点加入受信任集群清单，并在当前工作区内开放访问权限",
								Recommended: true,
							},
							{
								ID:          "reject",
								Label:       "拒绝登记",
								Description: "阻止将该服务器添加至集群",
							},
						},
					},
				},
			}

			askResult, askErr := a.executeAsk(ctx, sessionID, askReq)
			if askErr != nil {
				return nil, codedToolError("E_APPROVAL_CANCELLED", fmt.Errorf("server registration approval was cancelled: %w", askErr))
			}

			approved := false
			for _, ans := range askResult.Answers {
				if ans.QuestionID == "approve_cluster_add" {
					for _, sel := range ans.Selections {
						if sel.OptionID == "approve" {
							approved = true
							break
						}
					}
				}
			}

			if !approved {
				return nil, codedToolError("E_APPROVAL_REJECTED", fmt.Errorf("user rejected registering server %q to cluster", alias))
			}
		}

		// User approved (or non-interactive test run). Save node.
		node := SSHServerNode{
			ID:          newID(),
			Alias:       alias,
			Host:        host,
			Port:        port,
			Username:    username,
			AuthType:    "agent", // Default to agent or user configured
			Description: description,
			RiskLevel:   "low",
			Status:      "approved",
			CreatedBy:   "agent",
		}
		if err := a.SaveSSHServer(node); err != nil {
			return nil, codedToolError("E_SAVE_FAILED", fmt.Errorf("failed to save server node: %w", err))
		}

		// Also authorize for this workspace
		if workspace != "" {
			a.AuthorizeServerForWorkspace(workspace, alias)
		}

		return map[string]any{
			"ok":          true,
			"alias":       alias,
			"host":        host,
			"port":        port,
			"username":    username,
			"description": description,
			"authorized":  true,
			"status":      "approved",
			"note":        "server successfully registered and authorized for current workspace",
		}, nil

	default:
		return nil, codedToolError("E_BAD_SSH_CLUSTER", fmt.Errorf("action must be 'list' or 'add'"))
	}
}

// authorizeExistingSSHServer 处理“别名已登记”的 ssh_cluster add：向用户申请把该
// 节点授权给当前工作区，全程不碰已存节点。别名未授权时返回的 E_SERVER_UNAUTHORIZED
// 恢复提示正是指向这个调用，所以这里必须成功且无损——否则模型一照做就把用户的
// 密码/密钥口令抹掉（见 orch_remote.go 的 E_SERVER_UNAUTHORIZED 提示）。
func (a *App) authorizeExistingSSHServer(ctx context.Context, sessionID, workspace string, node SSHServerNode) (map[string]any, error) {
	if workspace == "" {
		return nil, codedToolError("E_BAD_SSH_CLUSTER", errors.New("no workspace for the current session; authorize the server from the chat footer instead"))
	}
	if a.IsServerAuthorizedForWorkspace(workspace, node.Alias) {
		return map[string]any{
			"ok":                true,
			"alias":             node.Alias,
			"alreadyRegistered": true,
			"authorized":        true,
			"note":              "server was already registered and is already authorized for the current workspace; nothing was modified",
		}, nil
	}
	if sessionID == "" {
		return nil, codedToolError("E_APPROVAL_REQUIRED", fmt.Errorf("server %q is already registered; the user must authorize it for the current workspace (no interactive session to ask in)", node.Alias))
	}

	port := node.Port
	if port <= 0 {
		port = 22
	}
	endpoint := fmt.Sprintf("%s:%d", nodeSSHHost(node), port)
	askReq := AskRequest{
		Questions: []AskQuestion{
			{
				ID: "approve_cluster_authorize",
				Question: fmt.Sprintf("服务器 [%s] 已在集群清单中（由 %s 配置）。\n- 地址: %s\n- 用途描述: %s\n\n是否把该节点授权给当前工作区访问？授权不会修改已存配置与凭据。",
					node.Alias, createdByLabel(node.CreatedBy), endpoint, node.Description),
				Options: []AskOption{
					{
						ID:          "approve",
						Label:       "授权当前工作区访问",
						Description: "把该节点加入当前工作区的可访问清单，已有凭据保持不变",
						Recommended: true,
					},
					{
						ID:          "reject",
						Label:       "不授权",
						Description: "保持当前工作区不能访问该节点",
					},
				},
			},
		},
	}

	askResult, askErr := a.executeAsk(ctx, sessionID, askReq)
	if askErr != nil {
		return nil, codedToolError("E_APPROVAL_CANCELLED", fmt.Errorf("server authorization approval was cancelled: %w", askErr))
	}
	approved := false
	for _, ans := range askResult.Answers {
		if ans.QuestionID == "approve_cluster_authorize" {
			for _, sel := range ans.Selections {
				if sel.OptionID == "approve" {
					approved = true
					break
				}
			}
		}
	}
	if !approved {
		return nil, codedToolError("E_APPROVAL_REJECTED", fmt.Errorf("user rejected authorizing server %q for the current workspace", node.Alias))
	}

	a.AuthorizeServerForWorkspace(workspace, node.Alias)
	return map[string]any{
		"ok":                true,
		"alias":             node.Alias,
		"alreadyRegistered": true,
		"authorized":        true,
		"note":              "existing server authorized for the current workspace; stored configuration and credentials were left untouched",
	}, nil
}

// createdByLabel 把节点的来源字段翻成人能读的短语（审批文案里用）。
func createdByLabel(createdBy string) string {
	if strings.EqualFold(strings.TrimSpace(createdBy), "agent") {
		return "模型登记"
	}
	return "用户配置"
}
