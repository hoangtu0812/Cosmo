import type {MessageToolCall, ToolApproval} from './api';

// Names, arguments and call IDs alone can repeat in later messages.
export function approvalsForCall(items: ToolApproval[], call: MessageToolCall, messageID?: string): ToolApproval[] {
  return items.filter((item) => call.approval_id
    ? item.id === call.approval_id
    : !!messageID && item.message_id === messageID && item.call_id === call.id);
}
