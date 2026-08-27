'use client';

import {useEffect, useState} from 'react';
import Image from 'next/image';
import {useRouter} from 'next/navigation';
import {ArrowRight, Blocks, Building2, Database, LoaderCircle, Lock, LogOut, Network, ShieldCheck, UserRound} from 'lucide-react';
import {Avatar} from '@astryxdesign/core/Avatar';
import {Badge} from '@astryxdesign/core/Badge';
import {ClickableCard} from '@astryxdesign/core/ClickableCard';
import {IconButton} from '@astryxdesign/core/IconButton';
import {api, APIError, User, Workspace} from '../lib/api';

export default function WorkspacesPage() {
  const router = useRouter();
  const [user, setUser] = useState<User | null>(null);
  const [items, setItems] = useState<Workspace[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');

  useEffect(() => {
    Promise.all([api.me(), api.workspaces()]).then(([me, result]) => {
      setUser(me.user);
      setItems(result.workspaces);
    }).catch((caught) => {
      if (caught instanceof APIError && caught.status === 401) router.replace('/');
      else setError(caught instanceof Error ? caught.message : 'Không thể tải workspace.');
    }).finally(() => setLoading(false));
  }, [router]);

  async function signOut() {
    await api.signOut();
    router.replace('/');
  }

  async function openWorkspace(workspaceID: string) {
    setError('');
    try {
      await api.selectWorkspace(workspaceID);
      router.push(`/chat?workspace=${encodeURIComponent(workspaceID)}`);
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : 'Không thể mở workspace.');
    }
  }

  return (
    <main className="workspace-page">
      <header className="workspace-header">
        <div className="app-brand"><span className="brand-symbol small" aria-hidden="true"><Image alt="" height={54} priority src="/cosmo-logo.png" width={54} /></span><div><strong>Cosmo</strong><span>Enterprise AI Platform</span></div><Badge label="CONTROL PLANE" variant="neutral" /></div>
        <div className="header-telemetry"><span><i /> Core online</span><span>AP-SE-01</span></div>
        {user && <div className="account-summary"><Avatar name={user.name} size="md" tooltip={user.email} /><div><strong>{user.name}</strong><span>{user.role === 'admin' ? 'Quản trị viên' : user.email}</span></div><Badge label={user.role === 'admin' ? 'ADMIN' : 'MEMBER'} variant={user.role === 'admin' ? 'purple' : 'neutral'} /><IconButton icon={<LogOut size={15} />} label="Đăng xuất" onClick={signOut} size="sm" variant="ghost" /></div>}
      </header>

      <section className="workspace-content">
        <div className="workspace-intro"><div className="intro-icon"><Blocks size={21} /></div><p className="section-kicker">Workspace router</p><h1>Chọn vùng ngữ cảnh</h1><p>Mỗi workspace là một ranh giới dữ liệu độc lập với model, công cụ, lịch sử và chính sách truy cập riêng.</p><div className="workspace-stats"><span><Network size={13} /> 2 contexts available</span><span><Database size={13} /> Isolation active</span><span><ShieldCheck size={13} /> Policy enforced</span></div></div>

        {loading && <div className="page-loading"><LoaderCircle className="spin" size={24} /> Đang tải workspace…</div>}
        {error && <div className="page-error">{error}</div>}
        {!loading && !error && <div className="workspace-grid">
          {items.map((workspace) => (
            <ClickableCard className="workspace-card" elevation="low" key={workspace.id} label={`Mở workspace ${workspace.name}`} onClick={() => openWorkspace(workspace.id)} padding={6}>
              <div className={`workspace-symbol ${workspace.type}`}>
                {workspace.type === 'personal' ? <UserRound size={23} /> : <Building2 size={23} />}
              </div>
              <div className="workspace-card-copy">
                <div><span className="workspace-type">{workspace.type === 'personal' ? 'PERSONAL CONTEXT' : 'ENTERPRISE CONTEXT'}</span><Badge label={workspace.role.toUpperCase()} variant={workspace.role === 'owner' ? 'purple' : 'neutral'} /></div>
                <h2>{workspace.name}</h2>
                <p>{workspace.type === 'personal' ? 'Dữ liệu và hội thoại chỉ dành cho bạn.' : 'Cộng tác với các thành viên được phân quyền.'}</p>
                <span className="workspace-meta"><i /> Ready <b>·</b> {workspace.type === 'personal' ? 'Private index' : 'Shared knowledge'}</span>
                <span className="enter-workspace">Enter context <ArrowRight size={15} /></span>
              </div>
            </ClickableCard>
          ))}
        </div>}

        <div className="workspace-security"><ShieldCheck size={18} /><div><strong>Zero cross-context leakage</strong><span>Quyền truy cập được xác minh lại trên mỗi yêu cầu; dữ liệu không tự động đi qua ranh giới workspace.</span></div><Badge icon={<Lock size={11} />} label="ENFORCED" variant="success" /></div>
      </section>
    </main>
  );
}
