'use client';

export const API_BASE = process.env.NEXT_PUBLIC_API_BASE_URL ?? 'http://localhost:8080';

export type User = {id: string; email: string; name: string; role: 'admin' | 'user'; last_workspace_id?: string; has_avatar: boolean};
export type Workspace = {
  id: string;
  name: string;
  description: string;
  slug: string;
  type: 'personal' | 'team' | 'project';
  role: string;
  icon?: string;
  has_icon_image?: boolean;
  model_configured?: boolean;
  model_alias?: string;
};
export type LLMSettings = {
  base_url: string;
  model: string;
  has_api_key: boolean;
  api_key_hint?: string;
  updated_at?: string;
  configured: boolean;
};
export type KnowledgeBase = {
  id: string;
  name: string;
  description: string;
  created_by_user_id?: string;
  created_by_name?: string;
  owner_workspace_id?: string;
  visibility: 'workspace' | 'selected' | 'everyone';
  layout_mode: 'auto' | 'always' | 'off';
	icon: string;
	tags: string[];
	retrieval_mode: 'semantic' | 'keyword' | 'hybrid';
	embedding_model: string;
	reranker_model: string;
	rerank_enabled: boolean;
	score_threshold: number;
	retrieval_top_k: number;
	chunk_size: number;
	chunk_overlap: number;
  created_at: string;
  access: 'owner' | 'viewer';
  version: number;
  has_unpublished_changes: boolean;
  is_mounted: boolean;
  installed_version: number;
  update_available: boolean;
  document_count: number;
	processing_count: number;
	failed_count: number;
  shared_count: number;
};
export type KnowledgeDocument = {
  id: string;
  kb_id: string;
  title: string;
  filename: string;
  content_type: string;
  size_bytes: number;
  version: number;
  status: 'pending' | 'processing' | 'ready' | 'failed';
  chunk_count: number;
  error?: string;
  created_at: string;
  updated_at: string;
};
export type KnowledgeIndexStatus = {total: number; ready: number; failed: number; pending: number; running: boolean};
export type KnowledgeShare = {workspace_id: string; name: string};
export type WorkspaceRef = {id: string; name: string};
// mode is what the gateway says a model is for ('embedding', 'rerank', 'chat').
// It is absent on gateways that do not report one.
export type GatewayModel = {
  id: string;
  mode?: string;
  provider?: string;
  supports_reasoning?: boolean;
  reasoning_efforts?: string[];
  default_reasoning_effort?: string;
};

// A tool is one HTTP integration: a base URL, a credential, and the actions
// underneath it. The credential itself never arrives here - only auth_hint,
// which is enough to show which key is configured.
export type ToolParameter = {
  name: string;
  description: string;
  type: 'string' | 'number' | 'boolean';
  in: 'query' | 'path' | 'body';
  is_required: boolean;
};

export type ToolAction = {
  id: string;
  tool_id: string;
  name: string;
  description: string;
  method: 'GET' | 'POST' | 'PUT' | 'PATCH' | 'DELETE';
  path: string;
  parameters: ToolParameter[];
  position: number;
  created_at: string;
  updated_at: string;
};

export type Tool = {
  id: string;
  name: string;
  description: string;
  icon: string;
  tags: string[];
  owner_user_id: string;
  owner_name: string;
  workspace_id: string;
  visibility: 'private' | 'workspace';
  base_url: string;
  /** 'http' for an API described by hand, 'mcp' for a server that describes
      itself, 'builtin' for one that reaches nothing at all. */
  kind: 'http' | 'mcp' | 'builtin';
  auth_type: 'none' | 'bearer' | 'header';
  auth_header_name: string;
  auth_hint: string;
  has_secret: boolean;
  action_count: number;
  is_editable: boolean;
  created_at: string;
  updated_at: string;
};

export type ToolUpdate = Partial<{
  name: string;
  description: string;
  icon: string;
  tags: string[];
  visibility: string;
  base_url: string;
  auth_type: string;
  auth_header_name: string;
  /** An empty string clears the stored credential; omit it to leave it alone. */
  auth_secret: string;
}>;

export type ToolCatalogEntry = {
  id: string;
  name: string;
  description: string;
  icon: string;
  category: string;
  /** 'builtin' reaches nothing and does its work on the server. */
  kind: 'http' | 'mcp' | 'builtin';
  base_url: string;
  actions: ToolAction[];
};

export type ToolCallResult = {
  status: number;
  duration_ms: number;
  body: string;
  is_truncated: boolean;
};

export type Agent = {
  id: string;
  name: string;
  introduction: string;
  avatar: string;
  tags: string[];
  owner_user_id: string;
  owner_name: string;
  workspace_id: string;
  visibility: 'private' | 'workspace';
  model: string;
  system_prompt: string;
  opening_line: string;
  preset_questions: string[];
  has_suggested_questions: boolean;
  is_memory_enabled: boolean;
  knowledge_base_ids: string[];
  has_avatar_image: boolean;
  // Whether this viewer may edit the agent: its author, or a workspace admin.
  is_editable: boolean;
  // What a save must carry back to prove it read the current draft.
  draft_revision: number;
  // 0 while the agent has never been published.
  published_version: number;
  published_version_id: string;
  has_unpublished_changes: boolean;
  created_at: string;
  updated_at: string;
};

// Every field is optional so the editor can save one tab without resending the
// rest; the server keeps what it is not sent.
export type AgentVersion = {
  id: string;
  agent_id: string;
  version_number: number;
  model: string;
  system_prompt: string;
  opening_line: string;
  preset_questions: string[];
  has_suggested_questions: boolean;
  is_memory_enabled: boolean;
  knowledge_base_ids: string[];
  changelog: string;
  published_by: string;
  created_at: string;
};

export type AgentUpdate = Partial<{
  name: string;
  introduction: string;
  avatar: string;
  tags: string[];
  visibility: 'private' | 'workspace';
  model: string;
  system_prompt: string;
  opening_line: string;
  preset_questions: string[];
  has_suggested_questions: boolean;
  is_memory_enabled: boolean;
  knowledge_base_ids: string[];
  // Sent so the server can refuse a save that would overwrite another editor.
  draft_revision: number;
}>;
export type DocumentEvent = {id: number; stage: string; message: string; done: number; total: number; created_at: string};
export type ProcessedDocumentChunk = {chunk_index: number; section: string; page: string; text: string};
export type KnowledgeDocumentDetail = {
  document: KnowledgeDocument;
  events: DocumentEvent[];
  inspection: {indexed: boolean; chunks: ProcessedDocumentChunk[]; total: number; truncated: boolean};
  index_error?: string;
};
export type Member = {user_id: string; email: string; name: string; role: string; joined_at: string};
export type Invitation = {id: string; email: string; role: string; expires_at: string; created_at: string; invite_url?: string};
export type Conversation = {
  id: string;
  workspace_id: string;
  agent_id?: string;
  title: string;
  // Empty when the conversation follows the draft rather than a frozen
  // version, and version_number is then 0.
  agent_version_id?: string;
  version_number?: number;
  created_at: string;
  updated_at: string;
};
export type Citation = {index: number; kb_id: string; document_id: string; title: string; source: string; section?: string; page?: string};
export type Message = {id: string; conversation_id: string; role: 'user' | 'assistant'; content: string; model?: string; citations?: Citation[]; created_at: string};
export type RunStatus = 'queued' | 'running' | 'waiting_approval' | 'succeeded' | 'failed' | 'cancelled' | 'timed_out';
export type Run = {
  id: string;
  workspace_id: string;
  project_id?: string;
  actor_user_id?: string;
  trigger_type: string;
  resource_type: string;
  resource_id: string;
  resource_version?: string;
  status: RunStatus;
  input: Record<string, unknown>;
  output: Record<string, unknown>;
  error_code?: string;
  error_message?: string;
  trace_id: string;
  created_at: string;
  queued_at: string;
  started_at?: string;
  finished_at?: string;
  cancelled_at?: string;
};
export type RunStep = {
  id: string;
  run_id: string;
  node_id?: string;
  type: string;
  name?: string;
  attempt: number;
  status: RunStatus;
  input: Record<string, unknown>;
  output: Record<string, unknown>;
  output_ref?: string;
  timeout_ms: number;
  error_code?: string;
  error_message?: string;
  created_at: string;
  started_at?: string;
  finished_at?: string;
};
export type RunEvent = {id: number; run_id: string; step_id?: string; sequence: number; type: string; payload: Record<string, unknown>; created_at: string};
export type AuthConfig = {local_signup_enabled: boolean; local_auth_enabled: boolean; entra_enabled: boolean; model_configured: boolean; model_alias: string};
export type AdminUser = {id: string; email: string; name: string; role: 'admin' | 'user'; provider: 'entra' | 'local'; workspace_count: number; created_at: string; has_avatar: boolean};
export type AuditEvent = {id: number; actor_name: string; actor_email: string; action: string; target_type: string; target_id: string; metadata: Record<string, unknown>; created_at: string};
export type SystemGatewaySettings = {base_url: string; has_api_key: boolean; api_key_hint?: string; configured: boolean};
export type SystemStatus = {entra_enabled: boolean; entra_tenant_id?: string; model_gateway_enabled: boolean; knowledge_enabled: boolean; cookie_secure: boolean; session_ttl: string; admin_email_count: number; configuration_source: string; embedding_model: string; reranker_model: string; system_gateway: SystemGatewaySettings};

type APIErrorShape = {error?: {message?: string}};

export class APIError extends Error {
  status: number;
  constructor(message: string, status: number) {
    super(message);
    this.status = status;
  }
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(`${API_BASE}${path}`, {
    ...init,
    credentials: 'include',
    headers: {'Content-Type': 'application/json', ...init?.headers},
  });
  if (!response.ok) {
    let body: APIErrorShape = {};
    try { body = await response.json() as APIErrorShape; } catch { /* ignore invalid error body */ }
    throw new APIError(body.error?.message ?? 'Không thể kết nối tới Cosmo API.', response.status);
  }
  if (response.status === 204) return undefined as T;
  return response.json() as Promise<T>;
}

/**
 * Multipart upload. The JSON helper cannot be reused here: setting
 * Content-Type by hand strips the boundary the browser generates, and the
 * server then reads the body as one undelimited part.
 */
async function upload<T>(path: string, form: FormData): Promise<T> {
  const response = await fetch(`${API_BASE}${path}`, {method: 'POST', credentials: 'include', body: form});
  if (!response.ok) {
    let body: APIErrorShape = {};
    try { body = await response.json() as APIErrorShape; } catch { /* ignore invalid error body */ }
    throw new APIError(body.error?.message ?? 'Không thể kết nối tới Cosmo API.', response.status);
  }
  if (response.status === 204) return undefined as T;
  return response.json() as Promise<T>;
}

export const api = {
  authConfig: () => request<AuthConfig>('/api/auth/config'),
  me: () => request<{user: User}>('/api/auth/me'),
  userAvatarURL: () => `${API_BASE}/api/auth/me/avatar`,
  adminUsers: () => request<{users: AdminUser[]}>('/api/admin/users'),
  updateAdminUser: (userID: string, role: 'admin' | 'user') => request<{id: string; role: 'admin' | 'user'}>(`/api/admin/users/${encodeURIComponent(userID)}`, {method: 'PATCH', body: JSON.stringify({role})}),
  auditEvents: () => request<{events: AuditEvent[]}>('/api/admin/audit-logs'),
  systemStatus: () => request<SystemStatus>('/api/admin/system'),
  updateSystemSettings: (body: {embedding_model: string; reranker_model: string; gateway_base_url: string; gateway_api_key?: string}) => request<SystemStatus>('/api/admin/system', {method: 'PUT', body: JSON.stringify(body)}),
  systemGatewayModels: (body: {base_url?: string; api_key?: string}) => request<{ok: boolean; message?: string; models: GatewayModel[]}>('/api/admin/system/models', {method: 'POST', body: JSON.stringify(body)}),
  reindexKnowledge: () => request<{queued: number}>('/api/admin/system/knowledge/reindex', {method: 'POST'}),
  knowledgeIndexStatus: () => request<KnowledgeIndexStatus>('/api/admin/system/knowledge/reindex'),
  signIn: (email: string, password: string, remember: boolean) => request<{user: User}>('/api/auth/signin', {method: 'POST', body: JSON.stringify({email, password, remember})}),
  signUp: (name: string, email: string, password: string) => request<{user: User}>('/api/auth/signup', {method: 'POST', body: JSON.stringify({name, email, password})}),
  signOut: () => request<void>('/api/auth/signout', {method: 'POST'}),
  workspaces: () => request<{workspaces: Workspace[]}>('/api/workspaces'),
  selectWorkspace: (workspaceID: string) => request<void>(`/api/workspaces/${encodeURIComponent(workspaceID)}/select`, {method: 'POST'}),
  conversations: (workspaceID: string) => request<{conversations: Conversation[]}>(`/api/conversations?workspace_id=${encodeURIComponent(workspaceID)}`),
  createConversation: (workspaceID: string, title = 'Cuộc trò chuyện mới') => request<{conversation: Conversation}>('/api/conversations', {method: 'POST', body: JSON.stringify({workspace_id: workspaceID, title})}),
  messages: (conversationID: string) => request<{messages: Message[]}>(`/api/conversations/${encodeURIComponent(conversationID)}/messages`),

  renameConversation: (conversationID: string, title: string) =>
    request<{conversation: {id: string; title: string}}>(`/api/conversations/${encodeURIComponent(conversationID)}`, {method: 'PATCH', body: JSON.stringify({title})}),
  deleteConversation: (conversationID: string) =>
    request<void>(`/api/conversations/${encodeURIComponent(conversationID)}`, {method: 'DELETE'}),
  updateWorkspace: (workspaceID: string, body: {name?: string; description?: string; icon?: string}) =>
    request<{workspace: Workspace}>(`/api/workspaces/${encodeURIComponent(workspaceID)}`, {method: 'PATCH', body: JSON.stringify(body)}),
  uploadWorkspaceIcon: (workspaceID: string, mime: string, data: string) =>
    request<void>(`/api/workspaces/${encodeURIComponent(workspaceID)}/icon`, {method: 'PUT', body: JSON.stringify({mime, data})}),
  deleteWorkspaceIcon: (workspaceID: string) =>
    request<void>(`/api/workspaces/${encodeURIComponent(workspaceID)}/icon`, {method: 'DELETE'}),
  workspaceIconURL: (workspaceID: string) => `${API_BASE}/api/workspaces/${encodeURIComponent(workspaceID)}/icon`,
  toolCatalog: (workspaceID?: string) =>
    request<{entries: ToolCatalogEntry[]; categories: string[]; installed: Record<string, string>}>(
      `/api/tools/catalog${workspaceID ? `?workspace=${encodeURIComponent(workspaceID)}` : ''}`,
    ),
  installCatalogTool: (entryID: string, workspaceID?: string) =>
    request<{tool: Tool; already_installed: boolean}>(`/api/tools/catalog/${encodeURIComponent(entryID)}${workspaceID ? `?workspace=${encodeURIComponent(workspaceID)}` : ''}`, {method: 'POST'}),
  draftToolActions: (toolID: string, description: string, workspaceID?: string) =>
    request<{actions: ToolAction[]}>(`/api/tools/${encodeURIComponent(toolID)}/draft${workspaceID ? `?workspace=${encodeURIComponent(workspaceID)}` : ''}`, {method: 'POST', body: JSON.stringify({description})}),
  importOpenAPI: (toolID: string, input: {url?: string; spec?: string}, workspaceID?: string) =>
    request<{actions: ToolAction[]}>(`/api/tools/${encodeURIComponent(toolID)}/openapi${workspaceID ? `?workspace=${encodeURIComponent(workspaceID)}` : ''}`, {method: 'POST', body: JSON.stringify(input)}),
  discoverMCPTools: (toolID: string, workspaceID?: string) =>
    request<{actions: ToolAction[]}>(`/api/tools/${encodeURIComponent(toolID)}/discover${workspaceID ? `?workspace=${encodeURIComponent(workspaceID)}` : ''}`, {method: 'POST'}),
  tools: (workspaceID?: string) =>
    request<{tools: Tool[]}>(`/api/tools${workspaceID ? `?workspace=${encodeURIComponent(workspaceID)}` : ''}`),
  tool: (toolID: string, workspaceID?: string) =>
    request<{tool: Tool; actions: ToolAction[]}>(`/api/tools/${encodeURIComponent(toolID)}${workspaceID ? `?workspace=${encodeURIComponent(workspaceID)}` : ''}`),
  createTool: (input: {name: string; description: string; icon: string; tags: string[]; base_url: string; kind?: string}, workspaceID?: string) =>
    request<{tool: Tool}>(`/api/tools${workspaceID ? `?workspace=${encodeURIComponent(workspaceID)}` : ''}`, {method: 'POST', body: JSON.stringify(input)}),
  updateTool: (toolID: string, changes: ToolUpdate, workspaceID?: string) =>
    request<{tool: Tool}>(`/api/tools/${encodeURIComponent(toolID)}${workspaceID ? `?workspace=${encodeURIComponent(workspaceID)}` : ''}`, {method: 'PATCH', body: JSON.stringify(changes)}),
  deleteTool: (toolID: string, workspaceID?: string) =>
    request<void>(`/api/tools/${encodeURIComponent(toolID)}${workspaceID ? `?workspace=${encodeURIComponent(workspaceID)}` : ''}`, {method: 'DELETE'}),
  saveToolAction: (toolID: string, actionID: string, input: Partial<ToolAction>, workspaceID?: string) =>
    request<{action: ToolAction}>(
      `/api/tools/${encodeURIComponent(toolID)}/actions${actionID ? `/${encodeURIComponent(actionID)}` : ''}${workspaceID ? `?workspace=${encodeURIComponent(workspaceID)}` : ''}`,
      {method: actionID ? 'PUT' : 'POST', body: JSON.stringify(input)},
    ),
  deleteToolAction: (toolID: string, actionID: string, workspaceID?: string) =>
    request<void>(`/api/tools/${encodeURIComponent(toolID)}/actions/${encodeURIComponent(actionID)}${workspaceID ? `?workspace=${encodeURIComponent(workspaceID)}` : ''}`, {method: 'DELETE'}),
  testToolAction: (toolID: string, actionID: string, args: Record<string, unknown>, workspaceID?: string) =>
    request<{result: ToolCallResult}>(
      `/api/tools/${encodeURIComponent(toolID)}/actions/${encodeURIComponent(actionID)}/test${workspaceID ? `?workspace=${encodeURIComponent(workspaceID)}` : ''}`,
      {method: 'POST', body: JSON.stringify({arguments: args})},
    ),
  agentTools: (agentID: string, workspaceID?: string) =>
    request<{tool_ids: string[]}>(`/api/agents/${encodeURIComponent(agentID)}/tools${workspaceID ? `?workspace=${encodeURIComponent(workspaceID)}` : ''}`),
  setAgentTools: (agentID: string, toolIDs: string[], workspaceID?: string) =>
    request<{tool_ids: string[]}>(
      `/api/agents/${encodeURIComponent(agentID)}/tools${workspaceID ? `?workspace=${encodeURIComponent(workspaceID)}` : ''}`,
      {method: 'PUT', body: JSON.stringify({tool_ids: toolIDs})},
    ),
  agents: (workspaceID?: string) =>
    request<{agents: Agent[]}>(`/api/agents${workspaceID ? `?workspace=${encodeURIComponent(workspaceID)}` : ''}`),
  agent: (agentID: string, workspaceID?: string) =>
    request<{agent: Agent}>(`/api/agents/${encodeURIComponent(agentID)}${workspaceID ? `?workspace=${encodeURIComponent(workspaceID)}` : ''}`),
  createAgent: (body: {name: string; introduction: string; avatar: string; tags: string[]; visibility: string; workspace_id: string}) =>
    request<{agent: Agent}>('/api/agents', {method: 'POST', body: JSON.stringify(body)}),
  updateAgent: (agentID: string, body: AgentUpdate, workspaceID?: string) =>
    request<{agent: Agent}>(`/api/agents/${encodeURIComponent(agentID)}${workspaceID ? `?workspace=${encodeURIComponent(workspaceID)}` : ''}`, {method: 'PATCH', body: JSON.stringify(body)}),
  // An agent's conversations are ordinary conversations stamped with its id,
  // so messages and streaming reuse the general chat endpoints untouched.
  agentConversations: (agentID: string, workspaceID?: string) =>
    request<{conversations: Conversation[]}>(`/api/agents/${encodeURIComponent(agentID)}/conversations${workspaceID ? `?workspace=${encodeURIComponent(workspaceID)}` : ''}`),
  // target 'draft' follows the working copy, which is what the editor's debug
  // panel wants; omitting it pins the conversation to the published version.
  startAgentConversation: (agentID: string, target: 'draft' | 'published', workspaceID?: string) =>
    request<{conversation: Conversation}>(`/api/agents/${encodeURIComponent(agentID)}/conversations${workspaceID ? `?workspace=${encodeURIComponent(workspaceID)}` : ''}`, {method: 'POST', body: JSON.stringify({target})}),
  agentAvatarURL: (agentID: string, workspaceID?: string) =>
    `${API_BASE}/api/agents/${encodeURIComponent(agentID)}/avatar${workspaceID ? `?workspace=${encodeURIComponent(workspaceID)}` : ''}`,
  uploadAgentAvatar: (agentID: string, mime: string, data: string, workspaceID?: string) =>
    request<void>(`/api/agents/${encodeURIComponent(agentID)}/avatar${workspaceID ? `?workspace=${encodeURIComponent(workspaceID)}` : ''}`, {method: 'PUT', body: JSON.stringify({mime, data})}),
  deleteAgentAvatar: (agentID: string, workspaceID?: string) =>
    request<void>(`/api/agents/${encodeURIComponent(agentID)}/avatar${workspaceID ? `?workspace=${encodeURIComponent(workspaceID)}` : ''}`, {method: 'DELETE'}),
  publishAgent: (agentID: string, changelog: string, workspaceID?: string) =>
    request<{agent: Agent}>(`/api/agents/${encodeURIComponent(agentID)}/publish${workspaceID ? `?workspace=${encodeURIComponent(workspaceID)}` : ''}`, {method: 'POST', body: JSON.stringify({changelog})}),
  agentVersions: (agentID: string, workspaceID?: string) =>
    request<{versions: AgentVersion[]}>(`/api/agents/${encodeURIComponent(agentID)}/versions${workspaceID ? `?workspace=${encodeURIComponent(workspaceID)}` : ''}`),
  deleteAgent: (agentID: string, workspaceID?: string) =>
    request<void>(`/api/agents/${encodeURIComponent(agentID)}${workspaceID ? `?workspace=${encodeURIComponent(workspaceID)}` : ''}`, {method: 'DELETE'}),
  knowledgeBases: (workspaceID?: string) =>
    request<{knowledge_bases: KnowledgeBase[]}>(`/api/knowledge${workspaceID ? `?workspace=${encodeURIComponent(workspaceID)}` : ''}`),
	createKnowledgeBase: (body: {name: string; description: string; workspace_id: string; icon?: string; tags?: string[]}) =>
		request<{knowledge_base: KnowledgeBase}>('/api/knowledge', {method: 'POST', body: JSON.stringify(body)}),
  publishKnowledgeBase: (kbID: string) =>
    request<{knowledge_base: KnowledgeBase}>(`/api/knowledge/${encodeURIComponent(kbID)}/publish`, {method: 'POST'}),
  knowledgeShares: (kbID: string) =>
    request<{shares: KnowledgeShare[]}>(`/api/knowledge/${encodeURIComponent(kbID)}/shares`),
  workspaceDirectory: () =>
    request<{workspaces: WorkspaceRef[]}>('/api/workspaces/directory'),
	updateKnowledgeBase: (kbID: string, body: Partial<Pick<KnowledgeBase,
		'name' | 'description' | 'visibility' | 'layout_mode' | 'icon' | 'tags' |
		'retrieval_mode' | 'embedding_model' | 'reranker_model' | 'rerank_enabled' |
		'score_threshold' | 'retrieval_top_k' | 'chunk_size' | 'chunk_overlap'
	>> & {workspaces?: string[]}) =>
    request<{knowledge_base: KnowledgeBase}>(`/api/knowledge/${encodeURIComponent(kbID)}`, {method: 'PATCH', body: JSON.stringify(body)}),
  deleteKnowledgeBase: (kbID: string) =>
    request<void>(`/api/knowledge/${encodeURIComponent(kbID)}`, {method: 'DELETE'}),
  uploadKnowledgeDocument: (kbID: string, file: File, title?: string) => {
    const form = new FormData();
    form.append('file', file);
    if (title) form.append('title', title);
    return upload<{document: KnowledgeDocument}>(`/api/knowledge/${encodeURIComponent(kbID)}/documents`, form);
  },
  knowledgeDocuments: (kbID: string) =>
    request<{documents: KnowledgeDocument[]}>(`/api/knowledge/${encodeURIComponent(kbID)}/documents`),
  knowledgeDocumentDetail: (kbID: string, documentID: string) =>
    request<KnowledgeDocumentDetail>(`/api/knowledge/${encodeURIComponent(kbID)}/documents/${encodeURIComponent(documentID)}/detail`),
  documentOriginalURL: (kbID: string, documentID: string) =>
    `${API_BASE}/api/knowledge/${encodeURIComponent(kbID)}/documents/${encodeURIComponent(documentID)}/original`,
  deleteKnowledgeDocument: (kbID: string, documentID: string) =>
    request<void>(`/api/knowledge/${encodeURIComponent(kbID)}/documents/${encodeURIComponent(documentID)}`, {method: 'DELETE'}),
  documentEvents: (kbID: string, documentID: string) =>
    request<{events: DocumentEvent[]}>(`/api/knowledge/${encodeURIComponent(kbID)}/documents/${encodeURIComponent(documentID)}/events`),
  documentStreamURL: (kbID: string, documentID: string) =>
    `${API_BASE}/api/knowledge/${encodeURIComponent(kbID)}/documents/${encodeURIComponent(documentID)}/stream`,
  workspaceKnowledge: (workspaceID: string) =>
    request<{knowledge_bases: KnowledgeBase[]}>(`/api/workspaces/${encodeURIComponent(workspaceID)}/knowledge`),
  mountKnowledge: (workspaceID: string, kbID: string) =>
    request<void>(`/api/workspaces/${encodeURIComponent(workspaceID)}/knowledge/${encodeURIComponent(kbID)}`, {method: 'PUT'}),
  unmountKnowledge: (workspaceID: string, kbID: string) =>
    request<void>(`/api/workspaces/${encodeURIComponent(workspaceID)}/knowledge/${encodeURIComponent(kbID)}`, {method: 'DELETE'}),
  createWorkspace: (name: string, description = '') => request<{workspace: Workspace}>('/api/workspaces', {method: 'POST', body: JSON.stringify({name, description})}),
  workspaceModels: (workspaceID: string) => request<{models: GatewayModel[]; default: string}>(`/api/workspaces/${encodeURIComponent(workspaceID)}/models`),
	workspaceKnowledgeModels: (workspaceID: string) =>
		request<{configured: boolean; message?: string; models: GatewayModel[]}>(`/api/workspaces/${encodeURIComponent(workspaceID)}/knowledge/models`),
  members: (workspaceID: string) => request<{members: Member[]}>(`/api/workspaces/${encodeURIComponent(workspaceID)}/members`),

  llmSettings: (workspaceID: string) => request<LLMSettings>(`/api/workspaces/${encodeURIComponent(workspaceID)}/settings/llm`),
  saveLLMSettings: (workspaceID: string, body: {base_url: string; model: string; api_key?: string | null}) =>
    request<LLMSettings>(`/api/workspaces/${encodeURIComponent(workspaceID)}/settings/llm`, {method: 'PUT', body: JSON.stringify(body)}),
  // POSTed so a key the operator has typed but not saved never lands in a URL.
  gatewayModels: (workspaceID: string, body: {base_url?: string; api_key?: string}) =>
    request<{ok: boolean; message?: string; models: string[]}>(
      `/api/workspaces/${encodeURIComponent(workspaceID)}/settings/llm/models`,
      {method: 'POST', body: JSON.stringify(body)},
    ),

  invitations: (workspaceID: string) => request<{invitations: Invitation[]}>(`/api/workspaces/${encodeURIComponent(workspaceID)}/invitations`),
  createInvitation: (workspaceID: string, email: string, role: string) =>
    request<{invitation: Invitation}>(`/api/workspaces/${encodeURIComponent(workspaceID)}/invitations`, {method: 'POST', body: JSON.stringify({email, role})}),
  revokeInvitation: (workspaceID: string, invitationID: string) =>
    request<void>(`/api/workspaces/${encodeURIComponent(workspaceID)}/invitations/${encodeURIComponent(invitationID)}`, {method: 'DELETE'}),
  acceptInvitation: (token: string) => request<{workspace: Workspace}>('/api/invitations/accept', {method: 'POST', body: JSON.stringify({token})}),
  runs: (workspaceID: string, limit = 50) => request<{runs: Run[]}>(`/api/runs?workspace=${encodeURIComponent(workspaceID)}&limit=${limit}`),
  run: (runID: string) => request<{run: Run}>(`/api/runs/${encodeURIComponent(runID)}`),
  runSteps: (runID: string) => request<{steps: RunStep[]}>(`/api/runs/${encodeURIComponent(runID)}/steps`),
  runEvents: (runID: string, after = 0) => request<{events: RunEvent[]}>(`/api/runs/${encodeURIComponent(runID)}/events?after=${after}`),
  cancelRun: (runID: string) => request<{run: Run}>(`/api/runs/${encodeURIComponent(runID)}/cancel`, {method: 'POST'}),
};

export type ChatOptions = {model?: string; reasoningEffort?: string};

export async function streamChat(
  conversationID: string,
  content: string,
  options: ChatOptions,
  handlers: {
    onMeta?: (data: {assistant_message_id: string; model: string; run_id?: string}) => void;
    onStatus?: (data: {stage: string; message: string}) => void;
    onSuggestions?: (data: {questions: string[]}) => void;
    onDelta: (content: string) => void;
    onDone?: (data: {message: Message}) => void;
  },
): Promise<void> {
  const response = await fetch(`${API_BASE}/api/conversations/${encodeURIComponent(conversationID)}/messages`, {
    method: 'POST',
    credentials: 'include',
    headers: {'Content-Type': 'application/json', Accept: 'text/event-stream'},
    body: JSON.stringify({content, model: options.model ?? '', reasoning_effort: options.reasoningEffort ?? ''}),
  });
  if (!response.ok) {
    let body: APIErrorShape = {};
    try { body = await response.json() as APIErrorShape; } catch { /* ignore invalid error body */ }
    throw new APIError(body.error?.message ?? 'Không thể gửi câu hỏi.', response.status);
  }
  if (!response.body) throw new APIError('Trình duyệt không hỗ trợ nhận dữ liệu streaming.', 500);

  const reader = response.body.getReader();
  const decoder = new TextDecoder();
  let buffer = '';
  while (true) {
    const {done, value} = await reader.read();
    if (done) break;
    buffer += decoder.decode(value, {stream: true});
    const frames = buffer.split('\n\n');
    buffer = frames.pop() ?? '';
    for (const frame of frames) {
      const lines = frame.split('\n');
      const event = lines.find((line) => line.startsWith('event:'))?.slice(6).trim();
      const rawData = lines.find((line) => line.startsWith('data:'))?.slice(5).trim();
      if (!event || !rawData) continue;
      const data = JSON.parse(rawData) as Record<string, unknown>;
      if (event === 'meta') handlers.onMeta?.(data as {assistant_message_id: string; model: string; run_id?: string});
      if (event === 'status') handlers.onStatus?.(data as {stage: string; message: string});
      if (event === 'suggestions') handlers.onSuggestions?.(data as {questions: string[]});
      if (event === 'delta') handlers.onDelta(String(data.content ?? ''));
      if (event === 'done') handlers.onDone?.(data as {message: Message});
      if (event === 'error') throw new APIError(String(data.message ?? 'Model Gateway không phản hồi.'), 502);
    }
  }
}
