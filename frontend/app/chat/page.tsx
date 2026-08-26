'use client';

import {FormEvent, useEffect, useMemo, useRef, useState} from 'react';
import Image from 'next/image';
import {useRouter} from 'next/navigation';
import {ThinkingOrb} from 'thinking-orbs';
import {AlertTriangle, ArrowLeft, Bot, ChevronDown, CircleUserRound, FileText, LogOut, Menu, MessageSquare, Plus, Send, ShieldCheck, Sparkles, X} from 'lucide-react';
import {api, APIError, AuthConfig, Conversation, Message, streamChat, User, Workspace} from '../lib/api';

const suggestions = [
  'Tóm tắt những điểm quan trọng trong quy trình vận hành.',
  'Tôi có thể sử dụng Cosmo để làm những công việc gì?',
  'Hướng dẫn tôi cách đặt câu hỏi có kết quả tốt hơn.',
];

export default function ChatPage() {
  const router = useRouter();
  const [user, setUser] = useState<User | null>(null);
  const [workspace, setWorkspace] = useState<Workspace | null>(null);
  const [config, setConfig] = useState<AuthConfig | null>(null);
  const [conversations, setConversations] = useState<Conversation[]>([]);
  const [conversationID, setConversationID] = useState('');
  const [messages, setMessages] = useState<Message[]>([]);
  const [draft, setDraft] = useState('');
  const [streaming, setStreaming] = useState(false);
  const [status, setStatus] = useState('');
  const [error, setError] = useState('');
  const [sidebarOpen, setSidebarOpen] = useState(false);
  const endRef = useRef<HTMLDivElement>(null);

  const activeConversation = useMemo(() => conversations.find((item) => item.id === conversationID), [conversations, conversationID]);

  useEffect(() => {
    const workspaceID = new URLSearchParams(window.location.search).get('workspace');
    if (!workspaceID) { router.replace('/workspaces'); return; }
    Promise.all([api.me(), api.workspaces(), api.authConfig(), api.conversations(workspaceID)]).then(([me, workspaceResult, authConfig, conversationResult]) => {
      const selected = workspaceResult.workspaces.find((item) => item.id === workspaceID);
      if (!selected) { router.replace('/workspaces'); return; }
      setUser(me.user);
      setWorkspace(selected);
      setConfig(authConfig);
      setConversations(conversationResult.conversations);
      if (conversationResult.conversations[0]) setConversationID(conversationResult.conversations[0].id);
    }).catch((caught) => {
      if (caught instanceof APIError && caught.status === 401) router.replace('/');
      else setError(caught instanceof Error ? caught.message : 'Không thể mở không gian trò chuyện.');
    });
  }, [router]);

  useEffect(() => {
    if (!conversationID) return;
    api.messages(conversationID).then((result) => setMessages(result.messages)).catch((caught) => setError(caught instanceof Error ? caught.message : 'Không thể tải hội thoại.'));
  }, [conversationID]);

  useEffect(() => { endRef.current?.scrollIntoView({behavior: 'smooth'}); }, [messages, streaming]);

  async function newConversation() {
    if (!workspace) return;
    const result = await api.createConversation(workspace.id);
    setConversations((current) => [result.conversation, ...current]);
    setConversationID(result.conversation.id);
    setMessages([]);
    setSidebarOpen(false);
  }

  async function submit(event?: FormEvent, suggestion?: string) {
    event?.preventDefault();
    const content = (suggestion ?? draft).trim();
    if (!content || streaming || !workspace) return;
    setDraft('');
    setError('');
    let targetID = conversationID;
    if (!targetID) {
      const result = await api.createConversation(workspace.id, content.slice(0, 100));
      targetID = result.conversation.id;
      setConversationID(targetID);
      setConversations((current) => [result.conversation, ...current]);
    }
    const optimisticUser: Message = {id: `local-${crypto.randomUUID()}`, conversation_id: targetID, role: 'user', content, created_at: new Date().toISOString()};
    const optimisticAssistant: Message = {id: `stream-${crypto.randomUUID()}`, conversation_id: targetID, role: 'assistant', content: '', created_at: new Date().toISOString()};
    setMessages((current) => [...current, optimisticUser, optimisticAssistant]);
    setStreaming(true);
    setStatus('Đang phân tích câu hỏi');
    try {
      await streamChat(targetID, content, {
        onMeta: () => setStatus('Đang soạn câu trả lời'),
        onDelta: (delta) => setMessages((current) => current.map((item) => item.id === optimisticAssistant.id ? {...item, content: item.content + delta} : item)),
        onDone: ({message}) => setMessages((current) => current.map((item) => item.id === optimisticAssistant.id ? message : item)),
      });
      const refreshed = await api.conversations(workspace.id);
      setConversations(refreshed.conversations);
    } catch (caught) {
      setMessages((current) => current.filter((item) => item.id !== optimisticAssistant.id));
      setError(caught instanceof Error ? caught.message : 'Không thể nhận câu trả lời.');
    } finally {
      setStreaming(false);
      setStatus('');
    }
  }

  async function signOut() { await api.signOut(); router.replace('/'); }

  return (
    <main className="chat-page">
      <aside className={`chat-sidebar ${sidebarOpen ? 'open' : ''}`}>
        <div className="sidebar-brand"><span className="brand-symbol tiny" aria-hidden="true"><Image alt="" height={48} priority src="/cosmo-logo.png" width={48} /></span><div><strong>Cosmo</strong><span>Enterprise AI</span></div><button aria-label="Đóng menu" className="mobile-close" onClick={() => setSidebarOpen(false)}><X size={18} /></button></div>
        <button className="new-chat" onClick={newConversation}><Plus size={17} /> Cuộc trò chuyện mới</button>
        <div className="conversation-heading"><span>Gần đây</span><MessageSquare size={14} /></div>
        <nav className="conversation-list">
          {conversations.map((item) => <button className={item.id === conversationID ? 'active' : ''} key={item.id} onClick={() => { setConversationID(item.id); setSidebarOpen(false); }}><MessageSquare size={15} /><span>{item.title}</span></button>)}
          {conversations.length === 0 && <p>Chưa có hội thoại nào.</p>}
        </nav>
        <div className="sidebar-bottom">
          <button onClick={() => router.push('/workspaces')}><ArrowLeft size={16} /> Đổi workspace</button>
          <button onClick={signOut}><LogOut size={16} /> Đăng xuất</button>
          {user && <div className="sidebar-account"><div className="avatar small-avatar">{user.name.charAt(0).toUpperCase()}</div><div><strong>{user.name}</strong><span>{user.email}</span></div></div>}
        </div>
      </aside>

      {sidebarOpen && <button aria-label="Đóng menu" className="sidebar-backdrop" onClick={() => setSidebarOpen(false)} />}

      <section className="chat-main">
        <header className="chat-header">
          <button className="menu-button" onClick={() => setSidebarOpen(true)}><Menu size={19} /></button>
          <button className="workspace-switch" onClick={() => router.push('/workspaces')}><span className="workspace-dot" /><div><small>Workspace</small><strong>{workspace?.name ?? 'Đang tải…'}</strong></div><ChevronDown size={16} /></button>
          <div className="header-center"><strong>{activeConversation?.title ?? 'Cuộc trò chuyện mới'}</strong><span><ShieldCheck size={13} /> Nội bộ</span></div>
          <div className="header-actions"><span className={`model-status ${config?.model_configured ? 'ready' : ''}`}><i />{config?.model_alias ?? 'company-general'}</span><CircleUserRound size={23} /></div>
        </header>

        {!config?.model_configured && config && <div className="model-banner"><AlertTriangle size={17} /><span><strong>Model Gateway chưa được cấu hình.</strong> Giao diện và lịch sử đã sẵn sàng; thêm cấu hình LLM trong <code>.env</code> để nhận câu trả lời thật.</span></div>}
        {error && <div className="chat-error"><AlertTriangle size={17} /><span>{error}</span><button onClick={() => setError('')}><X size={15} /></button></div>}

        <div className="messages-area">
          {messages.length === 0 ? (
            <div className="chat-welcome">
              <div className="welcome-orb"><ThinkingOrb state="breathing" size={64} aria-label="Cosmo sẵn sàng" /></div>
              <p className="section-kicker">Cosmo Assistant</p>
              <h1>Xin chào{user ? `, ${user.name.split(' ').slice(-1)[0]}` : ''}.</h1>
              <p>Bạn muốn khám phá điều gì trong workspace này?</p>
              <div className="suggestion-grid">
                {suggestions.map((suggestion, index) => <button key={suggestion} onClick={() => submit(undefined, suggestion)}><span>{index === 0 ? <FileText size={18} /> : index === 1 ? <Sparkles size={18} /> : <Bot size={18} />}</span>{suggestion}<Send size={15} /></button>)}
              </div>
            </div>
          ) : (
            <div className="message-thread">
              {messages.map((message) => (
                <article className={`message ${message.role}`} key={message.id}>
                  <div className="message-avatar">{message.role === 'assistant' ? <Bot size={18} /> : user?.name.charAt(0).toUpperCase()}</div>
                  <div><div className="message-author">{message.role === 'assistant' ? 'Cosmo' : user?.name}<span>{new Date(message.created_at).toLocaleTimeString('vi-VN', {hour: '2-digit', minute: '2-digit'})}</span></div><div className="message-content">{message.content || (streaming && message.role === 'assistant' ? <span className="thinking-inline"><ThinkingOrb state="composing" size={20} /> {status}</span> : '')}</div></div>
                </article>
              ))}
              <div ref={endRef} />
            </div>
          )}
        </div>

        <div className="composer-wrap">
          <form className="composer" onSubmit={submit}>
            <textarea aria-label="Câu hỏi" disabled={streaming} onChange={(event) => setDraft(event.target.value)} onKeyDown={(event) => { if (event.key === 'Enter' && !event.shiftKey) { event.preventDefault(); submit(); } }} placeholder="Hỏi Cosmo bất cứ điều gì…" rows={1} value={draft} />
            <button aria-label="Gửi câu hỏi" disabled={!draft.trim() || streaming} type="submit">{streaming ? <ThinkingOrb state="composing" size={20} /> : <Send size={18} />}</button>
          </form>
          <p>Cosmo có thể mắc lỗi. Hãy kiểm tra nguồn trước khi đưa ra quyết định quan trọng.</p>
        </div>
      </section>
    </main>
  );
}
