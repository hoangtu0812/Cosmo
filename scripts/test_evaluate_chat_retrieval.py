import unittest
import io
import copy
import json
from unittest.mock import patch
from evaluate_chat_retrieval import evaluate, score, summarize, validate_cases, quality_gates, compare_baseline, fingerprint


class RetrievalMetricsTest(unittest.TestCase):
    def test_missing_conflicting_evidence_fails_even_with_all_kbs_present(self):
        case = {'id': 'conflict', 'query': 'q', 'category': 'conflict',
                'relevant_document_ids': ['policy-a', 'policy-b'], 'required_kb_ids': ['one', 'two'],
                'evidence_groups': [['policy-a'], ['policy-b']]}
        validate_cases([case])
        result = score(case, {'passages': [{'document_id': 'policy-a', 'kb_id': 'one'},
                                         {'document_id': 'unrelated', 'kb_id': 'two'}]})
        self.assertEqual(result['source_coverage'], 1)
        self.assertEqual(result['evidence_coverage'], .5)
        violations = quality_gates([dict(result, id='conflict', status='ok')], {'min_evidence_coverage': 1})
        self.assertEqual(violations[0]['reason'], 'below_min_evidence_coverage')

    def test_invalid_labels_and_thresholds_fail_closed(self):
        valid = {'id': 'x', 'query': 'q', 'relevant_document_ids': ['a']}
        for change in ({'relevant_document_ids': [1]}, {'kb_ids': None}, {'category': 'conflict'},
                       {'required_kb_ids': ['a'], 'forbidden_kb_ids': ['a']}, {'evidence_groups': [['missing']]}):
            with self.assertRaises(ValueError):
                validate_cases([valid | change])
        for thresholds in ({'min_recall': float('nan')}, {'min_recall': True}, {'min_recall': 2}, {'typo': .5}):
            with self.assertRaises(ValueError):
                quality_gates([], thresholds)
        self.assertTrue(quality_gates([], {'min_evidence_coverage': 1}))

    def test_snapshot_provenance_and_report_redaction(self):
        payload = {'retrieval_contract': 'chat-go-v1', 'knowledge_mode': 'mixed', 'incomplete': False, 'duration_ms': 10,
                   'sources': [{'kb_id': 'one', 'snapshot_id': 'pin', 'status': 'ready', 'secret': 'do not copy'},
                               {'kb_id': 'two', 'status': 'empty'}],
                   'passages': [{'kb_id': 'one', 'snapshot_id': 'pin', 'document_id': 'a', 'text': 'private evidence'}]}
        case = {'id': 'x', 'query': 'private query', 'relevant_document_ids': ['a']}
        with patch('urllib.request.OpenerDirector.open', side_effect=lambda *a, **kw: io.StringIO(json.dumps(payload))):
            result = evaluate('http://localhost', 'workspace', [case], 'private session')[0]
            self.assertEqual(result['status'], 'ok')
            self.assertFalse(result['frozen_corpus'])
            self.assertEqual(result['knowledge_mode'], 'mixed')
            self.assertNotIn('private', json.dumps(result))
            self.assertNotIn('secret', json.dumps(result))
            payload['passages'][0]['snapshot_id'] = 'wrong'
            self.assertEqual(evaluate('http://localhost', 'workspace', [case], 'session')[0]['status'], 'failed')

    def test_baseline_requires_same_labels_and_frozen_corpus_and_detects_each_case(self):
        case = {'id': 'a', 'status': 'ok', 'frozen_corpus': True, 'corpus_fingerprint': 'pin', 'recall': 1}
        baseline = {'report_version': 2, 'workspace_id': 'w', 'contract': 'chat-go-v1',
                    'cases_fingerprint': fingerprint(['labels']), 'results': [case, dict(case, id='b', recall=0)]}
        candidate = copy.deepcopy(baseline)
        self.assertEqual(compare_baseline(candidate, baseline), [])
        candidate['results'][0]['recall'] = .5
        candidate['results'][1]['recall'] = .5
        self.assertEqual(compare_baseline(candidate, baseline)[0]['reason'], 'regression_recall')
        candidate = copy.deepcopy(baseline)
        candidate['results'][0]['frozen_corpus'] = False
        self.assertEqual(compare_baseline(candidate, baseline)[0]['reason'], 'baseline_requires_snapshot_sources')
        candidate['results'][0]['frozen_corpus'] = True
        candidate['results'][0]['corpus_fingerprint'] = 'new pin'
        self.assertEqual(compare_baseline(candidate, baseline)[0]['reason'], 'corpus_changed')
        candidate['cases_fingerprint'] = 'other labels'
        self.assertEqual(compare_baseline(candidate, baseline)[0]['reason'], 'incompatible_cases_fingerprint')

    def test_malformed_response_is_reported_without_aborting_cases(self):
        with patch('urllib.request.OpenerDirector.open', side_effect=lambda *a, **kw: io.StringIO('{"retrieval_contract":"chat-go-v1","passages":null}')):
            report = evaluate('http://localhost', 'workspace', [
                {'id': 'first', 'query': 'q', 'relevant_document_ids': []},
                {'id': 'second', 'query': 'q', 'relevant_document_ids': []},
            ], 'dummy')
        self.assertEqual([r['status'] for r in report], ['failed', 'failed'])

    def test_no_evidence_is_a_valid_response(self):
        result = score({'relevant_document_ids': []}, {'passages': []})
        self.assertFalse(result['unexpected_evidence'])

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
