import unittest
from evaluate_chat_retrieval import score, summarize


class RetrievalMetricsTest(unittest.TestCase):
    def test_duplicates_do_not_inflate_recall_and_order_matters(self):
        case = {'relevant_document_ids': ['a', 'b'], 'required_kb_ids': ['one', 'two']}
        response = {'passages': [{'document_id': d, 'kb_id': 'one'} for d in ['irrelevant', 'a', 'a']]}
        result = score(case, response)
        self.assertEqual(result['recall'], .5)
        self.assertEqual(result['precision'], .5)
        self.assertEqual(result['reciprocal_rank'], .5)
        self.assertEqual(result['source_coverage'], .5)

    def test_no_answer_and_forbidden_sources_are_explicit(self):
        result = score({'relevant_document_ids': [], 'forbidden_kb_ids': ['secret']},
                       {'passages': [{'document_id': 'a', 'kb_id': 'secret'}]})
        self.assertIsNone(result['recall'])
        self.assertTrue(result['unexpected_evidence'])
        self.assertTrue(result['forbidden_source_returned'])

    def test_failed_cases_stay_in_failure_denominator(self):
        report = summarize([{'status': 'ok', 'recall': 1, 'duration_ms': 20}, {'status': 'failed'}])
        self.assertEqual(report['cases'], 2)
        self.assertEqual(report['failure_rate'], .5)
        self.assertEqual(report['recall']['scored_cases'], 1)


if __name__ == '__main__':
    unittest.main()
