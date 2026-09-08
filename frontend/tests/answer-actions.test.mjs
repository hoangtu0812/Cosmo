import test from 'node:test';
import assert from 'node:assert/strict';
import {answerActions} from '../app/lib/answer-actions.ts';

test('legacy next actions become buttons without altering answer data', () => {
  const body = 'Thông tin PO 2200000000:\n\n| PO | Tiền tệ |\n| --- | --- |\n| 2200000000 | USD |';
  assert.deepEqual(answerActions(`${body}\n\nNếu cần, tôi có thể làm tiếp một trong các việc sau:\n\n1. Kiểm tra tóm tắt trạng thái/giao hàng của PO này\n2. **Xuất PO**\n3. Tra thêm PO khác`), {body, actions: ['Kiểm tra tóm tắt trạng thái/giao hàng của PO này', 'Xuất PO', 'Tra thêm PO khác']});
});

test('ordinary lists, code examples and nonterminal offers stay intact', () => {
  for (const body of ['Các trường chưa có dữ liệu:\n- Item\n- Material', '```md\nBước tiếp theo:\n1. Xóa dữ liệu\n```', 'Bước tiếp theo:\n1. Kiểm tra\n\nĐây là nội dung giải thích.']) {
    assert.deepEqual(answerActions(body), {body, actions: []});
  }
});

test('explicit offers to check more are actionable too', () => {
  assert.deepEqual(answerActions('PO 2200000000\n\nNếu bạn muốn, tôi có thể kiểm tra thêm:\n- Tóm tắt PO\n- Xuất PO'), {body:'PO 2200000000', actions:['Tóm tắt PO', 'Xuất PO']});
});
