'use client';

import {FormEvent, useEffect, useMemo, useRef, useState} from 'react';
import Image from 'next/image';
import {useRouter} from 'next/navigation';
import {ThinkingOrb} from 'thinking-orbs';
import {AlertTriangle, ArrowLeft, Bot, ChevronDown, FileText, Inbox, LibraryBig, LogOut, Menu, MessageSquare, Paperclip, Plus, Send, ShieldCheck, Sparkles, X} from 'lucide-react';
import {Avatar} from '@astryxdesign/core/Avatar';
import {Badge} from '@astryxdesign/core/Badge';
import {Button} from '@astryxdesign/core/Button';
import {ClickableCard} from '@astryxdesign/core/ClickableCard';
import {IconButton} from '@astryxdesign/core/IconButton';
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
    const requestedWorkspaceID = new URLSearchParams(window.location.search).get('workspace');
    Promise.all([api.me(), api.workspaces(), api.authConfig()]).then(async ([me, workspaceResult, authConfig]) => {
      const workspaceID = requestedWorkspaceID ?? me.user.last_workspace_id ?? workspaceResult.workspaces[0]?.id;
      const selected = workspaceResult.workspaces.find((item) => item.id === workspaceID);
      if (!selected) { router.replace('/workspaces'); return; }
      if (!requestedWorkspaceID) router.replace(`/chat?workspace=${encodeURIComponent(selected.id)}`);
      await api.selectWorkspace(selected.id);
      const conversationResult = await api.conversations(selected.id);
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
        <div className="sidebar-brand"><span className="brand-symbol tiny" aria-hidden="true"><Image alt="" height={48} priority src="/cosmo-logo.png" width={48} /></span><div><strong>Cosmo</strong><span>Enterprise AI</span></div><Badge label="BETA" variant="blue" /><IconButton className="mobile-close" icon={<X size={16} />} label="Đóng menu" onClick={() => setSidebarOpen(false)} size="sm" variant="ghost" /></div>
        <nav aria-label="Điều hướng Cosmo" className="product-navigation">
          <button className="product-nav-item active" type="button"><Sparkles size={16} /> Trợ lý</button>
          <button aria-disabled="true" className="product-nav-item is-unavailable" title="Sắp có" type="button"><Inbox size={16} /> Hộp thư</button>
          <button className="product-nav-item" onClick={() => router.push('/workspaces')} type="button"><LibraryBig size={16} /> Không gian làm việc</button>
        </nav>
        <div className="sidebar-section-title"><span>Workspace</span></div>
        <Button icon={<Plus size={15} />} label="Cuộc trò chuyện mới" onClick={newConversation} size="md" variant="secondary" width="100%" />
        <div className="conversation-heading"><span>Cuộc hội thoại gần đây</span><MessageSquare size={14} /></div>
        <nav className="conversation-list">
          {conversations.map((item) => <button className={item.id === conversationID ? 'active' : ''} key={item.id} onClick={() => { setConversationID(item.id); setSidebarOpen(false); }}><MessageSquare size={15} /><span>{item.title}</span></button>)}
          {conversations.length === 0 && <p>Chưa có hội thoại nào.</p>}
        </nav>
        <div className="sidebar-bottom">
          <Button icon={<ArrowLeft size={15} />} label="Đổi workspace" onClick={() => router.push('/workspaces')} size="sm" variant="ghost" width="100%" />
          <Button icon={<LogOut size={15} />} label="Đăng xuất" onClick={signOut} size="sm" variant="ghost" width="100%" />
          {user && <div className="sidebar-account"><Avatar name={user.name} size="sm" tooltip={user.email} /><div><strong>{user.name}</strong><span>{user.email}</span></div><span className="account-online" aria-label="Đang hoạt động" /></div>}
        </div>
      </aside>

      {sidebarOpen && <button aria-label="Đóng menu" className="sidebar-backdrop" onClick={() => setSidebarOpen(false)} />}

      <section className="chat-main">
        <header className="chat-header">
          <IconButton className="menu-button" icon={<Menu size={18} />} label="Mở menu" onClick={() => setSidebarOpen(true)} size="sm" variant="ghost" />
          <button className="workspace-switch" onClick={() => router.push('/workspaces')}><span className="workspace-dot" /><div><small>Workspace</small><strong>{workspace?.name ?? 'Đang tải…'}</strong></div><ChevronDown size={16} /></button>
          <div className="header-center"><strong>{activeConversation?.title ?? 'Cuộc trò chuyện mới'}</strong><Badge icon={<ShieldCheck size={11} />} label="INTERNAL" variant="success" /></div>
          <div className="header-actions"><Badge icon={<span className={`model-dot ${config?.model_configured ? 'ready' : ''}`} />} label={config?.model_alias ?? 'company-general'} variant="neutral" />{user && <Avatar name={user.name} size="sm" tooltip={user.email} />}</div>
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
                {suggestions.map((suggestion, index) => <ClickableCard className="suggestion-card" key={suggestion} label={suggestion} onClick={() => submit(undefined, suggestion)} padding={4}><span>{index === 0 ? <FileText size={17} /> : index === 1 ? <Sparkles size={17} /> : <Bot size={17} />}</span><strong>{index === 0 ? 'Tóm tắt tài liệu' : index === 1 ? 'Khám phá khả năng' : 'Prompt guide'}</strong><p>{suggestion}</p><Send size={14} /></ClickableCard>)}
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
            <div className="composer-actions"><span className="composer-context"><Paperclip size={14} /> Thêm ngữ cảnh</span><span className="composer-hint">Enter để gửi</span><IconButton icon={streaming ? <ThinkingOrb state="composing" size={20} /> : <Send size={16} />} isDisabled={!draft.trim() || streaming} label="Gửi câu hỏi" size="lg" type="submit" variant="primary" /></div>
          </form>
          <p>Cosmo có thể mắc lỗi. Hãy kiểm tra nguồn trước khi đưa ra quyết định quan trọng.</p>
        </div>
      </section>
    </main>
  );
}
