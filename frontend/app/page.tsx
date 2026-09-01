'use client';

import {FormEvent, useEffect, useState} from 'react';
import Image from 'next/image';
import {useRouter} from 'next/navigation';
import {ArrowRight, Eye, EyeOff, LockKeyhole} from 'lucide-react';
import {Banner} from '@astryxdesign/core/Banner';
import {Button} from '@astryxdesign/core/Button';
import {Card} from '@astryxdesign/core/Card';
import {CheckboxInput} from '@astryxdesign/core/CheckboxInput';
import {Divider} from '@astryxdesign/core/Divider';
import {FormLayout} from '@astryxdesign/core/FormLayout';
import {Heading} from '@astryxdesign/core/Heading';
import {HStack, Layout, LayoutContent, VStack} from '@astryxdesign/core/Layout';
import {Text} from '@astryxdesign/core/Text';
import {TextInput} from '@astryxdesign/core/TextInput';
import {API_BASE, api, AuthConfig} from './lib/api';
import {useTranslation} from './lib/i18n';

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
  const t = useTranslation();
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
      if (meResult.status === 'fulfilled') router.replace('/chat');
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
      router.push('/chat');
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : t('auth.failed'));
    } finally {
      setLoading(false);
    }
  }

  return (
    <main className="auth-shell">
      <div aria-hidden="true" className="auth-grid" />
      <div aria-hidden="true" className="auth-orb auth-orb-one" />
      <div aria-hidden="true" className="auth-orb auth-orb-two" />
      <Layout
        className="auth-layout"
        contentWidth={448}
        height="fill"
        padding={4}
        content={
          <LayoutContent>
            <VStack height="100%" vAlign="center">
              <Card className="auth-card" padding={6} width="100%">
                <VStack gap={5}>
                <VStack gap={3}>
                  <HStack gap={2} vAlign="center">
                    <span className="brand-symbol" aria-hidden="true">
                      <Image alt="" height={40} priority src="/cosmo-logo.png" width={40} />
                    </span>
                    <Text type="label">Cosmo</Text>
                  </HStack>
                  <Heading level={1} type="display-3">
                    {mode === 'signin' ? t('auth.welcome') : t('auth.create')}
                  </Heading>
                </VStack>

                {error && <Banner isDismissable onDismiss={() => setError('')} status="error" title={error} />}

                <VStack gap={2}>
                  <Button
                    endContent={<ArrowRight size={16} />}
                    href={config?.entra_enabled ? `${API_BASE}/api/auth/entra/start` : undefined}
                    icon={<span className="microsoft-mark" aria-hidden="true"><i /><i /><i /><i /></span>}
                    isDisabled={!config?.entra_enabled || loading}
                    label={t('auth.microsoft')}
                    size="lg"
                    tooltip={config?.entra_enabled ? t('auth.microsoftTooltip') : t('auth.microsoftDisabled')}
                    variant="secondary"
                    width="100%"
                  />
                  {config && !config.entra_enabled && (
                    <Text color="secondary" display="block" type="supporting">
                      {t('auth.microsoftHint')}
                    </Text>
                  )}
                </VStack>

                {config?.local_auth_enabled ? (
                  <>
                    <Divider label={t('auth.or')} />

                    <form onSubmit={submit}>
                      <FormLayout>
                        {mode === 'signup' && (
                          <TextInput htmlName="name" isRequired label={t('auth.name')} onChange={setName} size="lg" value={name} width="100%" />
                        )}
                        <TextInput htmlName="email" isRequired label={t('auth.email')} onChange={setEmail} placeholder="name@example.com" size="lg" type="email" value={email} width="100%" />
                        <TextInput
                          htmlName="password"
                          isRequired
                          label={t('auth.password')}
                          onChange={setPassword}
                          placeholder={mode === 'signup' ? t('auth.passwordRule') : undefined}
                          size="lg"
                          type={showPassword ? 'text' : 'password'}
                          value={password}
                          width="100%"
                        />
                        <HStack hAlign="end">
                          <Button
                            icon={showPassword ? <EyeOff size={14} /> : <Eye size={14} />}
                            label={showPassword ? t('auth.hidePassword') : t('auth.showPassword')}
                            onClick={() => setShowPassword((value) => !value)}
                            size="sm"
                            variant="ghost"
                          />
                        </HStack>

                        {mode === 'signin' && (
                          <HStack hAlign="between" vAlign="center">
                            <CheckboxInput label={t('auth.remember')} onChange={setRemember} size="sm" value={remember} />
                            <HStack gap={1.5} vAlign="center">
                              <LockKeyhole size={12} />
                              <Text color="secondary" type="supporting">{t('auth.encrypted')}</Text>
                            </HStack>
                          </HStack>
                        )}

                        <Button
                          isDisabled={loading || checkingSession}
                          isLoading={loading || checkingSession}
                          label={loading ? t('auth.signingIn') : mode === 'signin' ? t('auth.signIn') : t('auth.signUp')}
                          size="lg"
                          type="submit"
                          variant="primary"
                          width="100%"
                        />
                      </FormLayout>
                    </form>

                    <HStack gap={1} hAlign="center" vAlign="center">
                      <Text color="secondary">
                        {mode === 'signin' ? t('auth.noAccount') : t('auth.hasAccount')}
                      </Text>
                      <Button
                        label={mode === 'signin' ? t('auth.signUp') : t('auth.signIn')}
                        onClick={() => { setMode(mode === 'signin' ? 'signup' : 'signin'); setError(''); }}
                        size="sm"
                        variant="ghost"
                      />
                    </HStack>
                  </>
                ) : config?.entra_enabled ? (
                  <Text color="secondary" display="block" type="supporting">{t('auth.entraOnly')}</Text>
                ) : null}
                </VStack>
              </Card>
            </VStack>
          </LayoutContent>
        }
      />
    </main>
  );
}
