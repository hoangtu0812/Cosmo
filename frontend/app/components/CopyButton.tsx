'use client';

import {useEffect, useRef, useState} from 'react';
import {Check, Copy} from 'lucide-react';
import {IconButton} from '@astryxdesign/core/IconButton';
import {useTranslation} from '../lib/i18n';

/**
 * Copy, with the only feedback that matters: the icon becomes a tick.
 *
 * A toast for this would be louder than the action. The tick returns to the
 * copy icon on its own, so nothing has to be dismissed.
 */
export function CopyButton({text, label}: {text: string; label?: string}) {
  const t = useTranslation();
  const [isCopied, setIsCopied] = useState(false);
  // The revert is cancelled on unmount, so a message copied and then scrolled
  // out of the list does not set state on something that is gone.
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  useEffect(() => () => clearTimeout(timer.current), []);

  async function copy() {
    try {
      await navigator.clipboard.writeText(text);
    } catch {
      // Denied clipboard permission, or an insecure origin. Saying so would
      // be a dialog about a button nobody is waiting on.
      return;
    }
    setIsCopied(true);
    clearTimeout(timer.current);
    timer.current = setTimeout(() => setIsCopied(false), 1500);
  }

  return (
    <IconButton
      icon={isCopied ? <Check size={14} /> : <Copy size={14} />}
      label={isCopied ? t('common.copied') : label ?? t('common.copy')}
      onClick={() => void copy()}
      size="sm"
      variant="ghost"
    />
  );
}
