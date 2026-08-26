'use client';

import {useEffect, useState} from 'react';
import Image from 'next/image';
import {useRouter} from 'next/navigation';
import {ArrowRight, Building2, ChevronDown, LoaderCircle, Lock, LogOut, ShieldCheck, Sparkles, UserRound} from 'lucide-react';
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

  return (
    <main className="workspace-page">
      <header className="workspace-header">
        <div className="app-brand"><span className="brand-symbol small" aria-hidden="true"><Image alt="" height={54} priority src="/cosmo-logo.png" width={54} /></span><div><strong>Cosmo</strong><span>Enterprise AI Platform</span></div></div>
        {user && <div className="account-summary"><div className="avatar">{user.name.charAt(0).toUpperCase()}</div><div><strong>{user.name}</strong><span>{user.role === 'admin' ? 'Quản trị viên' : user.email}</span></div><ChevronDown size={16} /><button aria-label="Đăng xuất" onClick={signOut}><LogOut size={17} /></button></div>}
      </header>

      <section className="workspace-content">
        <div className="workspace-intro"><div className="intro-icon"><Sparkles size={22} /></div><p className="section-kicker">Không gian làm việc</p><h1>Chọn nơi bạn muốn bắt đầu</h1><p>Mỗi workspace có dữ liệu, công cụ, lịch sử hội thoại và chính sách truy cập riêng.</p></div>

        {loading && <div className="page-loading"><LoaderCircle className="spin" size={24} /> Đang tải workspace…</div>}
        {error && <div className="page-error">{error}</div>}
        {!loading && !error && <div className="workspace-grid">
          {items.map((workspace) => (
            <button className="workspace-card" key={workspace.id} onClick={() => router.push(`/chat?workspace=${encodeURIComponent(workspace.id)}`)}>
              <div className={`workspace-symbol ${workspace.type}`}>
                {workspace.type === 'personal' ? <UserRound size={23} /> : <Building2 size={23} />}
              </div>
              <div className="workspace-card-copy">
                <div><span className="workspace-type">{workspace.type === 'personal' ? 'Cá nhân' : 'Nhóm doanh nghiệp'}</span><span className="role-badge">{workspace.role}</span></div>
                <h2>{workspace.name}</h2>
                <p>{workspace.type === 'personal' ? 'Dữ liệu và hội thoại chỉ dành cho bạn.' : 'Cộng tác với các thành viên được phân quyền.'}</p>
                <span className="enter-workspace">Mở workspace <ArrowRight size={16} /></span>
              </div>
            </button>
          ))}
        </div>}

        <div className="workspace-security"><ShieldCheck size={19} /><div><strong>Quyền truy cập được kiểm tra ở mỗi yêu cầu</strong><span>Cosmo không tự động chia sẻ dữ liệu giữa các workspace.</span></div><Lock size={17} /></div>
      </section>
    </main>
  );
}
