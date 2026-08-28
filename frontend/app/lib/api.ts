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
  created_at: string;
  access: 'owner' | 'viewer';
  version: number;
  has_unpublished_changes: boolean;
  is_mounted: boolean;
  installed_version: number;
  update_available: boolean;
  document_count: number;
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
export type KnowledgeShare = {workspace_id: string; name: string};
export type WorkspaceRef = {id: string; name: string};
export type DocumentEvent = {id: number; stage: string; message: string; done: number; total: number; created_at: string};
export type Member = {user_id: string; email: string; name: string; role: string; joined_at: string};
export type Invitation = {id: string; email: string; role: string; expires_at: string; created_at: string; invite_url?: string};
export type Conversation = {id: string; workspace_id: string; title: string; created_at: string; updated_at: string};
export type Message = {id: string; conversation_id: string; role: 'user' | 'assistant'; content: string; model?: string; created_at: string};
export type AuthConfig = {local_signup_enabled: boolean; local_auth_enabled: boolean; entra_enabled: boolean; model_configured: boolean; model_alias: string};

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
  knowledgeBases: (workspaceID?: string) =>
    request<{knowledge_bases: KnowledgeBase[]}>(`/api/knowledge${workspaceID ? `?workspace=${encodeURIComponent(workspaceID)}` : ''}`),
  createKnowledgeBase: (name: string, description: string, workspaceID: string) =>
    request<{knowledge_base: KnowledgeBase}>('/api/knowledge', {method: 'POST', body: JSON.stringify({name, description, workspace_id: workspaceID})}),
  publishKnowledgeBase: (kbID: string) =>
    request<{knowledge_base: KnowledgeBase}>(`/api/knowledge/${encodeURIComponent(kbID)}/publish`, {method: 'POST'}),
  knowledgeShares: (kbID: string) =>
    request<{shares: KnowledgeShare[]}>(`/api/knowledge/${encodeURIComponent(kbID)}/shares`),
  workspaceDirectory: () =>
    request<{workspaces: WorkspaceRef[]}>('/api/workspaces/directory'),
  updateKnowledgeBase: (kbID: string, body: {name?: string; description?: string; visibility?: string; workspaces?: string[]}) =>
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
  workspaceModels: (workspaceID: string) => request<{models: string[]; default: string}>(`/api/workspaces/${encodeURIComponent(workspaceID)}/models`),
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
};

export type ChatOptions = {model?: string; reasoningEffort?: string};

export async function streamChat(
  conversationID: string,
  content: string,
  options: ChatOptions,
  handlers: {
    onMeta?: (data: {assistant_message_id: string; model: string}) => void;
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
      if (event === 'meta') handlers.onMeta?.(data as {assistant_message_id: string; model: string});
      if (event === 'delta') handlers.onDelta(String(data.content ?? ''));
      if (event === 'done') handlers.onDone?.(data as {message: Message});
      if (event === 'error') throw new APIError(String(data.message ?? 'Model Gateway không phản hồi.'), 502);
    }
  }
}
