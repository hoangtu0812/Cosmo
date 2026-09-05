"""Evaluate labeled cases through Cosmo's actual Go chat retrieval endpoint."""
import argparse
import hashlib
import json
import math
import os
import statistics
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path

METRICS = ('recall', 'precision', 'reciprocal_rank', 'ndcg', 'source_coverage', 'evidence_coverage')
CATEGORIES = ('single_source', 'multi_source', 'conflict', 'unanswerable', 'access_boundary')


def fingerprint(value):
    return hashlib.sha256(json.dumps(value, sort_keys=True, ensure_ascii=False, separators=(',', ':')).encode()).hexdigest()


def validate_cases(cases):
    ids = set()
    for case in cases:
        if not isinstance(case, dict):
            raise ValueError('Each case must be an object')
        if any(not isinstance(case.get(k), str) or not case[k].strip() for k in ('id', 'query')):
            raise ValueError('Each case needs an id and a query')
        if case['id'] in ids or len(case['query']) > 2000:
            raise ValueError('Duplicate case id or query longer than 2000 characters')
        ids.add(case['id'])
        for key in ('relevant_document_ids', 'required_kb_ids', 'forbidden_kb_ids', 'kb_ids'):
            if key != 'relevant_document_ids' and key not in case:
                continue
            values = case.get(key)
            if not isinstance(values, list) or any(not isinstance(v, str) or not v.strip() for v in values) or len(values) != len(set(values)):
                raise ValueError('Document and KB labels must be lists of unique nonempty strings')
        if len(case.get('kb_ids', [])) > 100:
            raise ValueError('At most 100 KB IDs per case')
        if set(case.get('required_kb_ids', [])) & set(case.get('forbidden_kb_ids', [])):
            raise ValueError('A source cannot be both required and forbidden')
        if 'category' in case and case['category'] not in CATEGORIES:
            raise ValueError('Unknown evaluation category')
        groups = case.get('evidence_groups', [])
        if not isinstance(groups, list):
            raise ValueError('Evidence groups must be a list')
        for group in groups:
            if not isinstance(group, list) or not group or any(not isinstance(d, str) for d in group) or not set(group) <= set(case['relevant_document_ids']):
                raise ValueError('Each evidence group must contain labeled relevant document IDs')
        if case.get('category') in ('multi_source', 'conflict') and (len(groups) < 2 or len(case.get('required_kb_ids', [])) < 2):
            raise ValueError('Multi-source/conflict cases need at least two evidence groups and required KBs')
        if case.get('category') == 'unanswerable' and case['relevant_document_ids']:
            raise ValueError('Unanswerable cases must have no relevant documents')
    if not cases:
        raise ValueError('No labeled cases supplied')


def score(case, response):
    # Document-level binary relevance; repeated chunks do not inflate hits.
    documents = list(dict.fromkeys(p['document_id'] for p in response['passages']))
    relevant = set(case['relevant_document_ids'])
    hits = [index + 1 for index, doc in enumerate(documents) if doc in relevant]
    dcg = sum(1 / math.log2(rank + 1) for rank in hits)
    ideal = sum(1 / math.log2(i + 2) for i in range(min(len(relevant), len(documents))))
    required = set(case.get('required_kb_ids', []))
    found = {p['kb_id'] for p in response['passages']}
    groups = case.get('evidence_groups', [])
    return {
        'recall': len(hits) / len(relevant) if relevant else None,
        'precision': len(hits) / len(documents) if documents else 0.0,
        'reciprocal_rank': 1 / hits[0] if hits else 0.0,
        'ndcg': dcg / ideal if ideal else (0.0 if relevant else None),
        'source_coverage': len(required & found) / len(required) if required else None,
        'evidence_coverage': sum(bool(set(group) & set(documents)) for group in groups) / len(groups) if groups else None,
        'unexpected_evidence': bool(documents) if not relevant else None,
        'forbidden_source_returned': bool(found & set(case.get('forbidden_kb_ids', []))),
    }


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


def evaluate(base, workspace, cases, cookie):
    opener = urllib.request.build_opener(NoRedirect())
    endpoint = base.rstrip('/') + '/api/workspaces/' + urllib.parse.quote(workspace, safe='') + '/knowledge/retrieve'
    results = []
    for case in cases:
        result = {'id': case['id'], 'category': case.get('category', 'uncategorized')}
        body = {'query': case['query']}
        if 'kb_ids' in case:
            body['kb_ids'] = case['kb_ids']
        request = urllib.request.Request(endpoint, data=json.dumps(body).encode(), headers={
            'Content-Type': 'application/json', 'Cookie': 'cosmo_session=' + cookie,
        })
        try:
            with opener.open(request, timeout=45) as response:
                payload = json.load(response)
            if payload.get('retrieval_contract') != 'chat-go-v1':
                raise ValueError('unexpected retrieval contract')
            if not isinstance(payload.get('incomplete'), bool) or not isinstance(payload.get('duration_ms'), (int, float)) or payload['duration_ms'] < 0:
                raise ValueError('Invalid retrieval status')
            sources = [{key: source[key] for key in ('kb_id', 'status')} | {'snapshot_id': source.get('snapshot_id', '')}
                       for source in payload['sources']]
            if any(not isinstance(s[k], str) for s in sources for k in ('kb_id', 'status', 'snapshot_id')):
                raise ValueError('Invalid source identity')
            identities = sorted((s['kb_id'], s['snapshot_id']) for s in sources)
            if len({s['kb_id'] for s in sources}) != len(sources):
                raise ValueError('Duplicate source identity')
            modes = {'snapshot' if pin else 'live' for _, pin in identities}
            mode = 'mixed' if len(modes) > 1 else next(iter(modes), 'live')
            if payload.get('knowledge_mode') != mode:
                raise ValueError('Retrieval mode disagrees with source identities')
            for passage in payload['passages']:
                if (passage['kb_id'], passage.get('snapshot_id', '')) not in identities or not isinstance(passage['document_id'], str):
                    raise ValueError('Passage provenance disagrees with sources')
            result.update(score(case, payload))
            result.update(incomplete=payload['incomplete'], duration_ms=payload['duration_ms'], sources=sources,
                          knowledge_mode=mode, corpus_fingerprint=fingerprint(identities),
                          frozen_corpus=bool(identities) and modes == {'snapshot'})
            result['document_ids'] = list(dict.fromkeys(p['document_id'] for p in payload['passages']))
            result['status'] = 'partial' if payload['incomplete'] else 'ok'
        except (urllib.error.URLError, TimeoutError, ValueError, KeyError, TypeError, AttributeError) as error:
            # Never copy URLs, credentials, passages or query text into reports.
            result.update(status='failed', error_type=type(error).__name__)
        results.append(result)
    return results


def summarize(results):
    failures = sum(r['status'] == 'failed' for r in results)
    partial = sum(r['status'] == 'partial' for r in results)
    summary = {'cases': len(results), 'failed': failures, 'partial': partial,
               'failure_rate': failures / len(results) if results else None}
    for metric in METRICS:
        values = [r[metric] for r in results if r.get(metric) is not None]
        summary[metric] = {'mean_on_responses': statistics.mean(values) if values else None, 'scored_cases': len(values)}
    latency = sorted(r['duration_ms'] for r in results if 'duration_ms' in r)
    summary['latency_ms'] = {p: latency[min(len(latency)-1, math.ceil(len(latency)*q)-1)] if latency else None
                             for p, q in [('p50', .5), ('p95', .95)]}
    summary['forbidden_source_cases'] = sum(bool(r.get('forbidden_source_returned')) for r in results)
    summary['unexpected_evidence_cases'] = sum(bool(r.get('unexpected_evidence')) for r in results)
    return summary


def quality_gates(results, thresholds):
    allowed = {'min_' + m for m in METRICS} | {'max_unexpected_evidence_rate', 'max_p95_ms'}
    for key, value in thresholds.items():
        if key not in allowed or isinstance(value, bool) or not isinstance(value, (int, float)) or not math.isfinite(value) or value < 0 or (key != 'max_p95_ms' and value > 1):
            raise ValueError('Invalid quality threshold')
    violations = []
    for result in results:
        if result['status'] != 'ok' or result.get('forbidden_source_returned'):
            violations.append({'id': result['id'], 'reason': 'retrieval_failure_or_forbidden_source'})
        for metric in METRICS:
            limit = thresholds.get('min_' + metric)
            value = result.get(metric)
            if limit is not None and value is not None and value < limit:
                violations.append({'id': result['id'], 'reason': 'below_min_' + metric, 'actual': value, 'threshold': limit})
    summary = summarize(results)
    for metric in METRICS:
        if 'min_' + metric in thresholds and not summary[metric]['scored_cases']:
            violations.append({'reason': 'no_labels_for_' + metric})
    unanswerable = [r for r in results if r.get('unexpected_evidence') is not None]
    if 'max_unexpected_evidence_rate' in thresholds:
        rate = sum(r['unexpected_evidence'] for r in unanswerable) / len(unanswerable) if unanswerable else None
        if rate is None or rate > thresholds['max_unexpected_evidence_rate']:
            violations.append({'reason': 'unexpected_evidence_rate', 'actual': rate})
    if 'max_p95_ms' in thresholds:
        p95 = summary['latency_ms']['p95']
        if p95 is None or p95 > thresholds['max_p95_ms']:
            violations.append({'reason': 'latency_p95', 'actual': p95})
    return violations


def compare_baseline(report, baseline, tolerance=0):
    # An average can hide losing one side of a conflict: compare each labeled case.
    errors = []
    for key in ('report_version', 'workspace_id', 'contract', 'cases_fingerprint'):
        if report.get(key) != baseline.get(key) or key not in baseline:
            return [{'reason': 'incompatible_' + key}]
    previous = {r['id']: r for r in baseline['results']}
    if len(previous) != len(baseline['results']) or set(previous) != {r['id'] for r in report['results']}:
        return [{'reason': 'incompatible_case_ids'}]
    for current in report['results']:
        old = previous[current['id']]
        reason = None
        if current['status'] != 'ok' or old['status'] != 'ok' or old.get('forbidden_source_returned'):
            reason = 'incomplete_candidate_or_baseline'
        elif not current.get('frozen_corpus') or not old.get('frozen_corpus'):
            reason = 'baseline_requires_snapshot_sources'
        elif current['corpus_fingerprint'] != old.get('corpus_fingerprint'):
            reason = 'corpus_changed'
        if reason:
            errors.append({'id': current['id'], 'reason': reason})
            continue
        for metric in METRICS:
            before, after = old.get(metric), current.get(metric)
            if (before is None) != (after is None) or (before is not None and after + tolerance < before):
                errors.append({'id': current['id'], 'reason': 'regression_' + metric, 'before': before, 'after': after})
        if current.get('unexpected_evidence') and not old.get('unexpected_evidence'):
            errors.append({'id': current['id'], 'reason': 'regression_unexpected_evidence'})
    return errors


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--base-url', default='http://localhost:8080')
    parser.add_argument('--workspace', required=True)
    parser.add_argument('--cases', type=Path, required=True)
    parser.add_argument('--output', type=Path, required=True)
    parser.add_argument('--revision', required=True, help='Deployed code revision being evaluated')
    parser.add_argument('--thresholds', type=Path, help='Explicit quality gates in a JSON object')
    parser.add_argument('--baseline', type=Path, help='Approved report using the same labels and immutable snapshot sources')
    parser.add_argument('--regression-tolerance', type=float, default=0, help='Allowed per-case absolute metric drop (0..1)')
    args = parser.parse_args()
    cookie = os.environ.get('COSMO_EVAL_SESSION', '')
    if not cookie:
        parser.error('Set COSMO_EVAL_SESSION to an authorized session token')
    base = urllib.parse.urlsplit(args.base_url)
    if base.scheme not in ('http', 'https') or not base.netloc or base.path not in ('', '/') or base.username or base.password or base.query or base.fragment:
        parser.error('Base URL must be an HTTP(S) origin without credentials or query')
    try:
        cases = [json.loads(line) for line in args.cases.read_text(encoding='utf-8-sig').splitlines() if line.strip()]
        validate_cases(cases)
        thresholds = json.loads(args.thresholds.read_text(encoding='utf-8-sig')) if args.thresholds else {}
        if not isinstance(thresholds, dict):
            raise ValueError('Thresholds must be an object')
        quality_gates([], thresholds)  # Validate before sending requests.
        if not 0 <= args.regression_tolerance <= 1:
            raise ValueError('Regression tolerance must be between 0 and 1')
        baseline = json.loads(args.baseline.read_text(encoding='utf-8-sig')) if args.baseline else None
    except (ValueError, OSError) as error:
        parser.error('Invalid evaluation inputs: ' + type(error).__name__)
    results = evaluate(args.base_url, args.workspace, cases, cookie)
    report = {'report_version': 2, 'revision': args.revision, 'workspace_id': args.workspace, 'contract': 'chat-go-v1',
              'cases_fingerprint': fingerprint(sorted(cases, key=lambda c: c['id'])),
              'thresholds': thresholds, 'summary': summarize(results), 'results': results,
              'categories': {category: summarize([r for r in results if r['category'] == category])
                             for category in sorted({r['category'] for r in results})}}
    violations = quality_gates(results, thresholds)
    if baseline is not None:
        try:
            comparison = compare_baseline(report, baseline, args.regression_tolerance)
        except (KeyError, TypeError, AttributeError, ValueError):
            comparison = [{'reason': 'malformed_baseline'}]
        report['baseline_comparison'] = {'revision': baseline.get('revision') if isinstance(baseline, dict) else None,
                                         'tolerance': args.regression_tolerance, 'violations': comparison}
        violations.extend(comparison)
    report['gate'] = {'passed': not violations, 'violations': violations,
                      'quality_thresholds_supplied': bool(thresholds), 'baseline_supplied': baseline is not None}
    args.output.write_text(json.dumps(report, ensure_ascii=False, indent=2), encoding='utf-8')
    print(json.dumps(report['summary'], ensure_ascii=False))
    return 0 if report['gate']['passed'] else 1


if __name__ == '__main__':
    raise SystemExit(main())
