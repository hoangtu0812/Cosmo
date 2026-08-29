# Đo chất lượng truy xuất Knowledge Base

Mọi tham số còn lại trong knowledge plane — rerank bao nhiêu ứng viên, cắt điểm
ở đâu, một tài liệu được góp mấy chunk — hiện đều là phỏng đoán. Chúng còn là
phỏng đoán cho tới khi cùng một bộ câu hỏi được hỏi hai cấu hình rồi so kết quả.
Tài liệu này hướng dẫn dựng bộ câu hỏi đó và dùng nó.

Nguyên tắc xuyên suốt: **con số tuyệt đối không có nghĩa gì.** `recall 0.74`
không nói lên điều gì. `0.74` so với `0.61` của cùng bộ câu hỏi trước khi đổi
mới là bằng chứng.

---

## 1. Toàn cảnh

| Thành phần | Ở đâu | Việc |
|---|---|---|
| Định nghĩa chỉ số | `rag-service/app/metrics.py` | recall / precision / MRR / nDCG, thuần, có unit test |
| Bộ chạy đo | `rag-service/app/evaluate.py` | hỏi từng câu, chấm điểm, in báo cáo, so với lần trước |
| Bộ câu hỏi | `rag-service/eval/questions.json` | tài sản bạn tự bồi đắp, nên commit vào repo |
| Báo cáo | `rag-service/eval/reports/` | kết quả từng lần chạy, đã gitignore |
| Log truy xuất | bảng `knowledge_retrieval_log` | câu hỏi thật của người dùng, nguyên liệu để soạn bộ câu hỏi |

Thư mục `rag-service/eval` được bind-mount vào container tại `/srv/eval`, nên
sửa file câu hỏi trên máy là có hiệu lực ngay, không cần build lại.

---

## 2. Quy trình đầy đủ

### Bước 1 — Bật log truy xuất

Mặc định **tắt**, vì nó lưu đúng những gì người dùng gõ. Bật trong `.env`:

```bash
KNOWLEDGE_RETRIEVAL_LOG=true
```

```bash
docker compose up -d backend
```

Từ lúc này mỗi lần chat có truy xuất sẽ sinh một dòng trong
`knowledge_retrieval_log`: câu hỏi, các KB đã tìm, và từng passage trả về kèm
điểm số và **retriever nào tìm ra nó** (`dense`, `sparse`, hoặc cả hai).

Chạy vài ngày để có dữ liệu thật. Đây là bước tốn thời gian nhất và không thể
rút ngắn — bộ câu hỏi bịa ra không đo được cái gì đáng đo.

### Bước 2 — Đãi câu hỏi thật từ log

Câu hỏi hay nhất để đưa vào bộ đo là câu **truy xuất đang làm tệ**: điểm cao
nhất thấp, hoặc không trả về gì.

```sql
-- 50 câu gần nhất mà truy xuất trả về điểm cao nhất dưới 0.5
SELECT created_at,
       query,
       jsonb_array_length(passages) AS passages,
       COALESCE((SELECT MAX((p->>'score')::float) FROM jsonb_array_elements(passages) p), 0) AS best
FROM knowledge_retrieval_log
ORDER BY best ASC, created_at DESC
LIMIT 50;
```

```sql
-- Câu hỏi lặp lại nhiều nhất: sửa được những câu này là đáng giá nhất
SELECT lower(btrim(query)) AS question, COUNT(*) AS lan
FROM knowledge_retrieval_log
WHERE created_at > NOW() - INTERVAL '30 days'
GROUP BY 1 ORDER BY lan DESC LIMIT 30;
```

```sql
-- Nhánh lexical có đang đóng góp không?
SELECT COUNT(*) FILTER (WHERE passages @> '[{"matched": ["dense", "sparse"]}]') AS ca_hai,
       COUNT(*) AS tong
FROM knowledge_retrieval_log;
```

Chạy nhanh bằng:

```bash
docker compose exec db psql -U cosmo -d cosmo
```

### Bước 3 — Tra document id

Bộ câu hỏi chấm điểm **theo tài liệu**, không theo chunk. Chunk nào của cuốn sổ
tay trả lời câu hỏi là chi tiết của cách chia đoạn; còn đúng cuốn sổ tay có được
trả về hay không mới là điều một câu hỏi có thể khẳng định trung thực.

```sql
SELECT d.id AS document_id, d.title, d.kb_id, k.name AS kb
FROM knowledge_documents d JOIN knowledge_bases k ON k.id = d.kb_id
WHERE d.status = 'ready' AND d.title ILIKE '%an toàn%';
```

### Bước 4 — Soạn bộ câu hỏi

Chép file mẫu rồi thay id thật:

```bash
cp rag-service/eval/questions.example.json rag-service/eval/questions.json
```

```json
{
  "questions": [
    {
      "id": "atsk-01",
      "query": "Quy trình an toàn khi vào khu vực hàn cần những bước gì?",
      "kb_ids": ["kb_a1b2c3"],
      "relevant": ["doc_x1y2z3"]
    },
    {
      "id": "bom-p101a",
      "query": "P-101A bảo dưỡng định kỳ bao lâu một lần?",
      "kb_ids": ["kb_a1b2c3"],
      "relevant": { "doc_quytrinh": 3, "doc_nhactoi": 1 }
    }
  ]
}
```

| Trường | Bắt buộc | Ý nghĩa |
|---|---|---|
| `id` | không | Nhãn ngắn hiện trong báo cáo. Bỏ trống thì tự đánh `q1`, `q2`… |
| `query` | **có** | Câu hỏi, viết đúng như người dùng thật sẽ gõ |
| `kb_ids` | **có** | Các KB được phép tìm. Đây là danh sách đã qua phân quyền, bộ đo không tự suy ra |
| `relevant` | **có** | Mảng document id (mức độ = 1), hoặc object `{document_id: mức độ}` |

**Mức độ liên quan** dùng cho nDCG: `3` cho tài liệu trả lời thẳng câu hỏi, `1`
cho tài liệu chỉ nhắc tới. Không bắt buộc dùng — mảng phẳng là đủ để bắt đầu.

Bộ chạy **từ chối** câu hỏi thiếu `query`, thiếu `relevant`, hoặc thiếu
`kb_ids`, và nêu đích danh `id` của nó. Một câu hỏi không chấm được sẽ luôn ăn
điểm 0 và kéo trung bình xuống vì lý do chẳng liên quan gì tới truy xuất.

**Bao nhiêu câu là đủ?** Dưới 20 câu thì nhiễu lớn hơn tín hiệu — đổi một tham
số có thể làm chỉ số nhảy chỉ vì một câu. 30–50 câu là mức bắt đầu đáng tin.
Trộn cả câu dễ lẫn câu đang làm tệ; một bộ toàn câu dễ sẽ luôn báo 0.95 và
không bao giờ phát hiện được hồi quy.

### Bước 5 — Chạy đo

```bash
docker compose exec rag python -m app.evaluate --questions eval/questions.json --k 10 --gateway-base-url "$LLM_BASE_URL" --embedding-model text-embedding-3-large --reranker-model cohere-rerank-multilingual --gateway-api-key "$LLM_API_KEY"
```

Gọn hơn: đặt sẵn biến môi trường trong `.env` (`GATEWAY_BASE_URL`,
`GATEWAY_API_KEY`, `EMBEDDING_MODEL`, `RERANKER_MODEL`) rồi chạy

```bash
docker compose exec rag python -m app.evaluate --questions eval/questions.json
```

> **Model phải trùng với model đã dựng index.** Đo một collection nhúng bằng
> model A bằng vector của model B cho ra những con số **trông giống** chất lượng
> truy xuất nhưng thật ra là lệch số chiều. Bộ chạy tự kiểm tra và dừng với
> thông báo rõ ràng nếu lệch — nhưng nếu hai model cùng số chiều thì nó không
> thể phát hiện, nên vẫn phải tự cẩn thận.

### Bước 6 — Đọc báo cáo

```
12/12 questions scored at k=10

metric          value   baseline    delta
recall         0.7500     0.6100  +0.1400
precision      0.2100     0.1800  +0.0300
mrr            0.6800     0.5200  +0.1600
ndcg           0.7100     0.5900  +0.1200

lexical retrieval contributed to 7/12 questions

question    recall    ndcg      rr  query
atsk-01       1.00    1.00    1.00  Quy trình an toàn khi vào khu vực hàn cần...
bom-p101a     0.50    0.63    1.00  P-101A bảo dưỡng định kỳ bao lâu một lần?
moment-siet   0.00    0.00    0.00  Mô men siết bu-lông mặt bích DN100
```

Dòng **`lexical retrieval contributed to`** là thứ đáng nhìn đầu tiên sau khi
bật hybrid: nếu nó đứng ở 0 thì index của bạn là dense-only có thêm vài bước
thừa — nhiều khả năng collection được tạo từ trước khi có sparse vector và cần
re-index.

Các dòng **recall 0.00** là danh sách việc cần làm: câu hỏi mà truy xuất không
trả về tài liệu đúng ở bất kỳ vị trí nào trong top-k.

### Bước 7 — So sánh giữa hai lần

Đây mới là mục đích thật sự.

```bash
# Lần chạy nền, trước khi đổi bất cứ thứ gì
docker compose exec rag python -m app.evaluate --report eval/reports/baseline.json

# ... đổi một tham số, re-index nếu cần ...

# Lần chạy sau, so thẳng với nền
docker compose exec rag python -m app.evaluate --baseline eval/reports/baseline.json --report eval/reports/rerank-50.json
```

**Mỗi lần chỉ đổi một thứ.** Đổi hai tham số cùng lúc rồi thấy chỉ số tăng thì
bạn không biết cái nào có công, và cái kia có thể đang làm hại.

---

## 3. Tham chiếu

### Cờ dòng lệnh

| Cờ | Mặc định | Ý nghĩa |
|---|---|---|
| `--questions` | `eval/questions.json` | Đường dẫn bộ câu hỏi |
| `--k` | `10` | Chấm điểm trên bao nhiêu kết quả đầu |
| `--report` | — | Ghi kết quả ra file JSON để làm nền cho lần sau |
| `--baseline` | — | File báo cáo cũ để so, in thêm cột delta |
| `--gateway-base-url` | env `GATEWAY_BASE_URL` | Base URL của LiteLLM |
| `--gateway-api-key` | env `GATEWAY_API_KEY` | Khoá gateway |
| `--embedding-model` | env `EMBEDDING_MODEL` | Phải trùng model đã dựng index |
| `--reranker-model` | env `RERANKER_MODEL` | Model rerank |

Thoát với mã `1` nếu có câu hỏi nào không hỏi được, `0` nếu chấm hết — dùng
được trong CI.

Lưu ý về `--k`: nó là số kết quả **được chấm**, còn số kết quả truy xuất **trả
về** do `RERANK_OUTPUT` quyết định (mặc định 12). Đặt `--k` lớn hơn
`RERANK_OUTPUT` thì phần dư luôn rỗng và recall bị chặn trần một cách giả tạo.

### Các chỉ số

| Chỉ số | Trả lời câu hỏi | Đọc thế nào |
|---|---|---|
| **recall@k** | Bao nhiêu phần tài liệu đúng đã được tìm ra? | Quan trọng nhất. Reranker sắp xếp lại được thứ nó nhận, nhưng không cứu được tài liệu chưa bao giờ trả về |
| **precision@k** | Bao nhiêu phần kết quả trả về là xứng đáng? | Chỉ đọc kèm recall. Một hệ thống trả đúng 1 kết quả may mắn có precision hoàn hảo |
| **MRR** | Kết quả đúng đầu tiên nằm ở vị trí nào? | Nhạy với thứ hạng theo cách recall không nhạy — hạng 1 và hạng 9 cùng recall nhưng khác hẳn trải nghiệm |
| **nDCG@k** | Thứ tự có tốt nhất có thể không? | Có phân mức độ. `1.0` nghĩa là không cách nào xếp tốt hơn |

Nhiều chunk của cùng một tài liệu được tính **một lần** trước khi chấm. Nếu
không, một tài liệu góp ba chunk sẽ được tính ba lần — đúng cái tình huống mà
bước diversity sinh ra để chặn.

Câu hỏi lỗi (gateway không tới được, v.v.) bị **loại khỏi trung bình**, không
tính là 0. Một điểm 0 vì gateway chết sẽ đọc thành hồi quy truy xuất chưa từng
xảy ra. Số câu lỗi được báo riêng.

### Bảng `knowledge_retrieval_log`

| Cột | Kiểu | Nội dung |
|---|---|---|
| `workspace_id` | TEXT | Workspace đã hỏi. Xoá workspace thì log theo đó biến mất |
| `query` | TEXT | Nguyên văn câu hỏi |
| `kb_ids` | TEXT[] | Các KB được phép tìm tại thời điểm đó |
| `passages` | JSONB | `[{document_id, kb_id, score, matched}]` |
| `created_at` | TIMESTAMPTZ | |

Ghi theo kiểu best-effort: một dòng log không ghi được **không** làm hỏng câu
trả lời cho người dùng.

**Lưu trữ là việc của bạn.** Bảng này chỉ lớn lên. Cắt định kỳ:

```sql
DELETE FROM knowledge_retrieval_log WHERE created_at < NOW() - INTERVAL '90 days';
```

---

## 4. Tham số đáng chỉnh

Chỉnh trong `.env`, khởi động lại `rag`, chạy lại bộ đo.

| Biến | Mặc định | Tăng lên thì | Đo bằng |
|---|---|---|---|
| `CANDIDATES_PER_KB` | 60 | Recall tăng, truy xuất chậm hơn | recall@k |
| `RERANK_INPUT` | 100 | Reranker có nhiều thứ để chọn hơn, nhưng request nặng hơn và dễ chạm trần Cohere | recall@k, độ trễ |
| `RERANK_OUTPUT` | 12 | Model được đọc nhiều đoạn hơn, tốn token, dễ nhiễu | precision, chất lượng trả lời |
| `MAX_CHUNKS_PER_DOCUMENT` | 3 | Quy trình dài không bị cắt cụt, nhưng một tài liệu dễ chiếm hết chỗ | recall@k cho câu hỏi quy trình dài |
| `CHUNK_SIZE` / `CHUNK_OVERLAP` | 900 / 150 | **Cần re-index**. Chunk lớn giữ ngữ cảnh, chunk nhỏ chính xác hơn | toàn bộ chỉ số |
| `AVERAGE_PASSAGE_TERMS` | 400 | Chuẩn hoá độ dài cho BM25. Hạ xuống nếu chunk ngắn | recall của câu hỏi chứa mã hiệu |

Đổi `CHUNK_SIZE`, `CHUNK_OVERLAP` hay embedding model đều **bắt buộc re-index**
— và phải chạy lại bộ đo sau khi re-index xong, không phải trong lúc đang chạy.

---

## 5. Bẫy thường gặp

**Đo trong lúc đang re-index.** Index chưa đầy, recall thấp giả tạo. Đợi bảng
tiến độ trong console admin báo hết `pending` rồi mới chạy.

**Bộ câu hỏi soạn bằng cách nhìn vào kết quả hiện tại.** Nếu bạn chọn tài liệu
"đúng" bằng cách xem hệ thống đang trả về gì, bạn đang đo xem hệ thống có nhất
quán với chính nó không, chứ không đo nó có đúng không. Chọn tài liệu đúng bằng
cách đọc tài liệu.

**Bộ câu hỏi không cập nhật.** Tài liệu bị xoá hay thay bản mới thì document id
trong bộ câu hỏi thành mồ côi và câu đó vĩnh viễn 0 điểm. Khi thấy một câu tụt
về 0 đột ngột, kiểm tra tài liệu còn tồn tại không trước khi đổ lỗi cho truy
xuất.

**So hai lần chạy khác `--k`.** Các con số không so được với nhau. Cột delta
vẫn in ra, và vẫn vô nghĩa.

**Kết luận từ một câu hỏi.** Một câu thay đổi thứ hạng là nhiễu. Chỉ đọc phần
trung bình khi quyết định, còn phần từng câu để tìm chỗ cần sửa.
