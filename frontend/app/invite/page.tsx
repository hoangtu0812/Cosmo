'use client';

import {useCallback, useEffect, useState} from 'react';
import {useRouter} from 'next/navigation';
import {Banner} from '@astryxdesign/core/Banner';
import {Button} from '@astryxdesign/core/Button';
import {Card} from '@astryxdesign/core/Card';
import {Heading} from '@astryxdesign/core/Heading';
import {Layout, LayoutContent, VStack} from '@astryxdesign/core/Layout';
import {api, APIError} from '../lib/api';
import {useTranslation} from '../lib/i18n';

export default function InvitePage() {
  const t = useTranslation();
  const router = useRouter();
  // Read once during render rather than setting state inside an effect.
  const [token] = useState(() =>
    typeof window === 'undefined' ? '' : new URLSearchParams(window.location.search).get('token') ?? '',
  );
  const [state, setState] = useState<'checking' | 'ready' | 'joining' | 'error'>(token ? 'checking' : 'error');
  const [error, setError] = useState('');

  useEffect(() => {
    if (!token) return;
    // The invite can only be accepted by a signed-in account, so send the
    // visitor through sign-in first and bring them back here afterwards.
    api.me()
      .then(() => setState('ready'))
      .catch((caught) => {
        if (caught instanceof APIError && caught.status === 401) {
          router.replace(`/?next=${encodeURIComponent(`/invite?token=${token}`)}`);
          return;
        }
        setError(caught instanceof Error ? caught.message : t('invite.sessionFailed'));
        setState('error');
      });
  }, [router, t, token]);

  const accept = useCallback(async () => {
    setState('joining');
    setError('');
    try {
      const result = await api.acceptInvitation(token);
      router.replace(`/chat?workspace=${encodeURIComponent(result.workspace.id)}`);
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : t('invite.joinFailed'));
      setState('error');
    }
  }, [router, t, token]);

  return (
    <Layout
      contentWidth={448}
      height="fill"
      padding={4}
      content={
        <LayoutContent>
          <VStack height="100%" vAlign="center">
            <Card padding={6} width="100%">
              <VStack gap={4}>
                <Heading level={1} type="display-3">{t('invite.title')}</Heading>

                {(error || !token) && <Banner status="error" title={error || t('invite.missingToken')} />}

                <Button
                  isDisabled={state !== 'ready'}
                  isLoading={state === 'checking' || state === 'joining'}
                  label={state === 'joining' ? t('invite.joining') : t('invite.accept')}
                  onClick={() => void accept()}
                  size="lg"
                  variant="primary"
                  width="100%"
                />
                <Button label={t('invite.toChat')} onClick={() => router.replace('/chat')} variant="ghost" width="100%" />
              </VStack>
            </Card>
          </VStack>
        </LayoutContent>
      }
    />
  );
}
