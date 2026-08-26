'use client';

import {FormEvent, useEffect, useState} from 'react';
import Image from 'next/image';
import {useRouter} from 'next/navigation';
import {AlertCircle, ArrowRight, Check, Eye, EyeOff, LoaderCircle, LockKeyhole, ShieldCheck} from 'lucide-react';
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
    const values = new FormData(event.currentTarget);
    setLoading(true);
    setError('');
    try {
      const email = String(values.get('email') ?? '');
      const password = String(values.get('password') ?? '');
      if (mode === 'signup') {
        await api.signUp(String(values.get('name') ?? ''), email, password);
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
        </div>

        <div className="brand-copy">
          <span className="eyebrow">AI nội bộ · An toàn · Có kiểm soát</span>
          <h1>Trí tuệ doanh nghiệp,<br />trong một không gian làm việc.</h1>
          <p>
            Trò chuyện với dữ liệu nội bộ, cộng tác cùng các AI Agent và truy cập
            công cụ nghiệp vụ theo đúng quyền của bạn.
          </p>
          <div className="trust-list">
            <div><Check size={17} /> Dữ liệu được phân quyền theo workspace</div>
            <div><Check size={17} /> Câu trả lời có nguồn trích dẫn rõ ràng</div>
            <div><Check size={17} /> Mọi hoạt động quan trọng đều được kiểm soát</div>
          </div>
        </div>

        <div className="brand-footer"><ShieldCheck size={18} /><span>Được bảo vệ bởi chính sách bảo mật doanh nghiệp</span></div>
      </section>

      <section className="auth-panel">
        <div className="environment-pill"><span /> Môi trường nội bộ</div>
        <div className="auth-card">
          <div className="mobile-brand"><span className="brand-symbol" aria-hidden="true"><Image alt="" height={64} priority src="/cosmo-logo.png" width={64} /></span><strong>Cosmo</strong></div>
          <header>
            <p className="section-kicker">Chào mừng bạn</p>
            <h2>{mode === 'signin' ? 'Đăng nhập vào Cosmo' : 'Tạo tài khoản Cosmo'}</h2>
            <p>{mode === 'signin' ? 'Sử dụng tài khoản công ty hoặc tài khoản Cosmo của bạn.' : 'Tạo tài khoản cá nhân để bắt đầu workspace của riêng bạn.'}</p>
          </header>

          {error && <div className="form-alert" role="alert"><AlertCircle size={17} /><span>{error}</span></div>}

          <button
            className="microsoft-button"
            disabled={!config?.entra_enabled || loading}
            onClick={() => { window.location.href = `${API_BASE}/api/auth/entra/start`; }}
            title={config?.entra_enabled ? 'Đăng nhập qua Microsoft Entra ID' : 'Cần cấu hình Azure AD trong .env'}
            type="button"
          >
            <span className="microsoft-mark" aria-hidden="true"><i /><i /><i /><i /></span>
            Tiếp tục với Microsoft
            <ArrowRight size={17} />
          </button>
          {config && !config.entra_enabled && <p className="provider-note">Quản trị viên cần cấu hình Microsoft Entra ID để bật lựa chọn này.</p>}

          <div className="divider"><span>hoặc dùng tài khoản Cosmo</span></div>

          <form className="auth-form" onSubmit={submit}>
            {mode === 'signup' && <label><span>Họ và tên</span><input autoComplete="name" name="name" placeholder="Nguyễn Văn An" required /></label>}
            <label><span>Email</span><input autoComplete="email" name="email" type="email" placeholder="tenban@congty.vn" required /></label>
            <label>
              <span>Mật khẩu</span>
              <div className="password-field">
                <input autoComplete={mode === 'signin' ? 'current-password' : 'new-password'} minLength={10} name="password" type={showPassword ? 'text' : 'password'} placeholder={mode === 'signin' ? 'Nhập mật khẩu' : 'Tối thiểu 10 ký tự, gồm chữ và số'} required />
                <button aria-label={showPassword ? 'Ẩn mật khẩu' : 'Hiện mật khẩu'} onClick={() => setShowPassword((value) => !value)} type="button">{showPassword ? <EyeOff size={18} /> : <Eye size={18} />}</button>
              </div>
            </label>

            {mode === 'signin' && <div className="form-options"><label className="remember"><input checked={remember} onChange={(event) => setRemember(event.target.checked)} type="checkbox" /> <span>Ghi nhớ đăng nhập</span></label><span className="secure-session">Phiên đăng nhập được mã hóa</span></div>}

            <button className="primary-button" disabled={loading || checkingSession} type="submit">
              {loading || checkingSession ? <LoaderCircle className="spin" size={18} /> : <LockKeyhole size={17} />}
              {loading ? 'Đang xác thực…' : mode === 'signin' ? 'Đăng nhập' : 'Tạo tài khoản'}
            </button>
          </form>

          <p className="switch-mode">{mode === 'signin' ? 'Chưa có tài khoản?' : 'Đã có tài khoản?'}{' '}<button onClick={() => { setMode(mode === 'signin' ? 'signup' : 'signin'); setError(''); }} type="button">{mode === 'signin' ? 'Đăng ký ngay' : 'Đăng nhập'}</button></p>
          <p className="legal-copy">Khi tiếp tục, bạn đồng ý tuân thủ chính sách sử dụng AI và bảo mật dữ liệu của doanh nghiệp.</p>
        </div>
      </section>
    </main>
  );
}
