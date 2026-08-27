'use client';

import {FormEvent, useEffect, useState} from 'react';
import Image from 'next/image';
import {useRouter} from 'next/navigation';
import {Activity, AlertCircle, ArrowRight, Braces, Check, Cpu, Database, Eye, EyeOff, LockKeyhole, Network, ShieldCheck} from 'lucide-react';
import {Badge} from '@astryxdesign/core/Badge';
import {Button} from '@astryxdesign/core/Button';
import {CheckboxInput} from '@astryxdesign/core/CheckboxInput';
import {TextInput} from '@astryxdesign/core/TextInput';
import {API_BASE, api, AuthConfig} from './lib/api';

const oauthErrors: Record<string, string> = {
  entra_not_configured: 'Microsoft Entra ID chưa được cấu hình trên máy chủ.',
  invalid_oauth_state: 'Phiên đăng nhập Microsoft không hợp lệ. Vui lòng thử lại.',
  token_exchange_failed: 'Microsoft không thể hoàn tất phiên đăng nhập.',
  missing_id_token: 'Microsoft không trả về thông tin định danh cần thiết.',
  invalid_id_token: 'Thông tin định danh Microsoft không hợp lệ.',
  invalid_claims: 'Không thể đọc thông tin tài khoản Microsoft.',
  email_required: 'Tài khoản Microsoft cần có email hợp lệ.',
  account_provision_failed: 'Không thể khởi tạo tài khoản Cosmo từ Microsoft Entra ID.',
};

export default function Home() {
  const router = useRouter();
  const [mode, setMode] = useState<'signin' | 'signup'>('signin');
  const [showPassword, setShowPassword] = useState(false);
  const [remember, setRemember] = useState(true);
  const [name, setName] = useState('');
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [config, setConfig] = useState<AuthConfig | null>(null);
  const [loading, setLoading] = useState(false);
  const [checkingSession, setCheckingSession] = useState(true);
  const [error, setError] = useState('');

  useEffect(() => {
    const oauthError = new URLSearchParams(window.location.search).get('auth_error');
    if (oauthError) {
      queueMicrotask(() => setError(oauthErrors[oauthError] ?? 'Đăng nhập Microsoft không thành công.'));
    }
    Promise.allSettled([api.authConfig(), api.me()]).then(([configResult, meResult]) => {
      if (configResult.status === 'fulfilled') setConfig(configResult.value);
      if (meResult.status === 'fulfilled') router.replace('/workspaces');
      setCheckingSession(false);
    });
  }, [router]);

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setLoading(true);
    setError('');
    try {
      if (mode === 'signup') {
        await api.signUp(name, email, password);
      } else {
        await api.signIn(email, password, remember);
      }
      router.push('/workspaces');
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : 'Không thể đăng nhập.');
    } finally {
      setLoading(false);
    }
  }

  return (
    <main className="auth-page">
      <section className="auth-brand" aria-label="Giới thiệu nền tảng">
        <div className="brand-topline">
          <span className="brand-symbol" aria-hidden="true"><Image alt="" height={64} priority src="/cosmo-logo.png" width={64} /></span>
          <div><strong>Cosmo</strong><span>Enterprise AI Platform</span></div>
          <Badge label="PRIVATE AI" variant="blue" />
        </div>

        <div className="brand-copy">
          <span className="eyebrow"><Activity size={13} /> Intelligence fabric / online</span>
          <h1>Một giao diện.<br />Toàn bộ trí tuệ<br />doanh nghiệp.</h1>
          <p>
            Hỏi dữ liệu, điều phối AI Agent và truy cập công cụ nghiệp vụ trong
            một lớp trải nghiệm được kiểm soát theo quyền của bạn.
          </p>
          <div className="system-visual" aria-hidden="true">
            <div className="system-grid" />
            <div className="system-core"><Braces size={20} /><span>ORCHESTRATION CORE</span><strong>Context engine</strong><small><i /> Operational</small></div>
            <div className="system-node node-data"><Database size={15} /><span>Knowledge</span><strong>Synced</strong></div>
            <div className="system-node node-agent"><Cpu size={15} /><span>Agents</span><strong>12 ready</strong></div>
            <div className="system-node node-policy"><ShieldCheck size={15} /><span>Policy</span><strong>Enforced</strong></div>
            <div className="system-line line-one" /><div className="system-line line-two" /><div className="system-line line-three" />
          </div>
          <div className="trust-list">
            <div><Check size={15} /> Workspace-aware access</div>
            <div><Check size={15} /> Governed model gateway</div>
            <div><Check size={15} /> Observable by default</div>
          </div>
        </div>

        <div className="brand-footer"><Network size={16} /><span>COSMO CORE</span><i /> <span>Secure enterprise runtime</span></div>
      </section>

      <section className="auth-panel">
        <div className="environment-pill"><span /> Internal environment <code>AP-SE-01</code></div>
        <div className="auth-card">
          <div className="mobile-brand"><span className="brand-symbol" aria-hidden="true"><Image alt="" height={64} priority src="/cosmo-logo.png" width={64} /></span><strong>Cosmo</strong></div>
          <header>
            <p className="section-kicker">Identity gateway</p>
            <h2>{mode === 'signin' ? 'Truy cập Cosmo' : 'Khởi tạo tài khoản'}</h2>
            <p>{mode === 'signin' ? 'Xác thực bằng danh tính doanh nghiệp hoặc tài khoản Cosmo.' : 'Tạo danh tính để bắt đầu workspace riêng của bạn.'}</p>
          </header>

          {error && <div className="form-alert" role="alert"><AlertCircle size={17} /><span>{error}</span></div>}

          <Button
            endContent={<ArrowRight size={16} />}
            href={config?.entra_enabled ? `${API_BASE}/api/auth/entra/start` : undefined}
            icon={<span className="microsoft-mark" aria-hidden="true"><i /><i /><i /><i /></span>}
            isDisabled={!config?.entra_enabled || loading}
            label="Tiếp tục với Microsoft"
            size="lg"
            tooltip={config?.entra_enabled ? 'Đăng nhập qua Microsoft Entra ID' : 'Cần cấu hình Azure AD trong .env'}
            variant="secondary"
            width="100%"
          />
          {config && !config.entra_enabled && <p className="provider-note">Quản trị viên cần cấu hình Microsoft Entra ID để bật lựa chọn này.</p>}

          <div className="divider"><span>Cosmo identity</span></div>

          <form className="auth-form" onSubmit={submit}>
            {mode === 'signup' && <TextInput className="auth-field" htmlName="name" isRequired label="Họ và tên" onChange={setName} placeholder="Nguyễn Văn An" size="lg" value={name} width="100%" />}
            <TextInput className="auth-field" htmlName="email" isRequired label="Email doanh nghiệp" onChange={setEmail} placeholder="tenban@congty.vn" size="lg" type="email" value={email} width="100%" />
            <div className="password-field">
              <TextInput className="auth-field" htmlName="password" isRequired label="Mật khẩu" onChange={setPassword} placeholder={mode === 'signin' ? 'Nhập mật khẩu' : 'Tối thiểu 10 ký tự, gồm chữ và số'} size="lg" type={showPassword ? 'text' : 'password'} value={password} width="100%" />
              <button aria-label={showPassword ? 'Ẩn mật khẩu' : 'Hiện mật khẩu'} className="password-toggle" onClick={() => setShowPassword((value) => !value)} type="button">{showPassword ? <EyeOff size={15} /> : <Eye size={15} />}{showPassword ? 'Ẩn' : 'Hiện'}</button>
            </div>

            {mode === 'signin' && <div className="form-options"><CheckboxInput label="Ghi nhớ thiết bị này" onChange={setRemember} size="sm" value={remember} /><span className="secure-session"><LockKeyhole size={12} /> Encrypted session</span></div>}

            <Button icon={<LockKeyhole size={16} />} isDisabled={loading || checkingSession} isLoading={loading || checkingSession} label={loading ? 'Đang xác thực…' : mode === 'signin' ? 'Đăng nhập an toàn' : 'Tạo tài khoản'} size="lg" type="submit" variant="primary" width="100%" />
          </form>

          <p className="switch-mode">{mode === 'signin' ? 'Chưa có tài khoản?' : 'Đã có tài khoản?'}{' '}<button onClick={() => { setMode(mode === 'signin' ? 'signup' : 'signin'); setError(''); }} type="button">{mode === 'signin' ? 'Đăng ký ngay' : 'Đăng nhập'}</button></p>
          <p className="legal-copy">Khi tiếp tục, bạn đồng ý tuân thủ chính sách sử dụng AI và bảo mật dữ liệu của doanh nghiệp.</p>
        </div>
      </section>
    </main>
  );
}
