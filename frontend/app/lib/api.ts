'use client';

export const API_BASE = process.env.NEXT_PUBLIC_API_BASE_URL ?? 'http://localhost:8080';

export type User = {id: string; email: string; name: string; role: 'admin' | 'user'; last_workspace_id?: string};
export type Workspace = {id: string; name: string; slug: string; type: 'personal' | 'team' | 'project'; role: string};
export type Conversation = {id: string; workspace_id: string; title: string; created_at: string; updated_at: string};
export type Message = {id: string; conversation_id: string; role: 'user' | 'assistant'; content: string; created_at: string};
export type AuthConfig = {local_signup_enabled: boolean; entra_enabled: boolean; model_configured: boolean; model_alias: string};

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

export const api = {
  authConfig: () => request<AuthConfig>('/api/auth/config'),
  me: () => request<{user: User}>('/api/auth/me'),
  signIn: (email: string, password: string, remember: boolean) => request<{user: User}>('/api/auth/signin', {method: 'POST', body: JSON.stringify({email, password, remember})}),
  signUp: (name: string, email: string, password: string) => request<{user: User}>('/api/auth/signup', {method: 'POST', body: JSON.stringify({name, email, password})}),
  signOut: () => request<void>('/api/auth/signout', {method: 'POST'}),
  workspaces: () => request<{workspaces: Workspace[]}>('/api/workspaces'),
  selectWorkspace: (workspaceID: string) => request<void>(`/api/workspaces/${encodeURIComponent(workspaceID)}/select`, {method: 'POST'}),
  conversations: (workspaceID: string) => request<{conversations: Conversation[]}>(`/api/conversations?workspace_id=${encodeURIComponent(workspaceID)}`),
  createConversation: (workspaceID: string, title = 'Cuộc trò chuyện mới') => request<{conversation: Conversation}>('/api/conversations', {method: 'POST', body: JSON.stringify({workspace_id: workspaceID, title})}),
  messages: (conversationID: string) => request<{messages: Message[]}>(`/api/conversations/${encodeURIComponent(conversationID)}/messages`),
};

export async function streamChat(
  conversationID: string,
  content: string,
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
    body: JSON.stringify({content}),
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
