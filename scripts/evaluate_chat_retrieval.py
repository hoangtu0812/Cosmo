"""Evaluate labeled cases through Cosmo's actual Go chat retrieval endpoint."""
import argparse
import json
import math
import os
import statistics
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path


def score(case, response):
    # Document-level binary relevance; repeated chunks do not inflate hits.
    documents = list(dict.fromkeys(p['document_id'] for p in response['passages']))
    relevant = set(case['relevant_document_ids'])
    hits = [index + 1 for index, doc in enumerate(documents) if doc in relevant]
    dcg = sum(1 / math.log2(rank + 1) for rank in hits)
    ideal = sum(1 / math.log2(i + 2) for i in range(min(len(relevant), len(documents))))
    required = set(case.get('required_kb_ids', []))
    found = {p['kb_id'] for p in response['passages']}
    return {
        'recall': len(hits) / len(relevant) if relevant else None,
        'precision': len(hits) / len(documents) if documents else 0.0,
        'reciprocal_rank': 1 / hits[0] if hits else 0.0,
        'ndcg': dcg / ideal if ideal else (0.0 if relevant else None),
        'source_coverage': len(required & found) / len(required) if required else None,
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
        result = {'id': case['id']}
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
            result.update(score(case, payload))
            result.update({key: payload[key] for key in ('incomplete', 'duration_ms', 'sources', 'knowledge_mode')})
            result['document_ids'] = list(dict.fromkeys(p['document_id'] for p in payload['passages']))
            result['status'] = 'partial' if payload['incomplete'] else 'ok'
        except (urllib.error.URLError, TimeoutError, ValueError, KeyError) as error:
            # Never copy URLs, credentials, passages or query text into reports.
            result.update(status='failed', error_type=type(error).__name__)
        results.append(result)
    return results


def summarize(results):
    failures = sum(r['status'] == 'failed' for r in results)
    partial = sum(r['status'] == 'partial' for r in results)
    summary = {'cases': len(results), 'failed': failures, 'partial': partial,
               'failure_rate': failures / len(results) if results else None}
    for metric in ('recall', 'precision', 'reciprocal_rank', 'ndcg', 'source_coverage'):
        values = [r[metric] for r in results if r.get(metric) is not None]
        summary[metric] = {'mean_on_responses': statistics.mean(values) if values else None, 'scored_cases': len(values)}
    latency = sorted(r['duration_ms'] for r in results if 'duration_ms' in r)
    summary['latency_ms'] = {p: latency[min(len(latency)-1, math.ceil(len(latency)*q)-1)] if latency else None
                             for p, q in [('p50', .5), ('p95', .95)]}
    summary['forbidden_source_cases'] = sum(bool(r.get('forbidden_source_returned')) for r in results)
    return summary


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--base-url', default='http://localhost:8080')
    parser.add_argument('--workspace', required=True)
    parser.add_argument('--cases', type=Path, required=True)
    parser.add_argument('--output', type=Path, required=True)
    parser.add_argument('--revision', required=True, help='Deployed code revision being evaluated')
    args = parser.parse_args()
    cookie = os.environ.get('COSMO_EVAL_SESSION', '')
    if not cookie:
        parser.error('Set COSMO_EVAL_SESSION to an authorized session token')
    base = urllib.parse.urlsplit(args.base_url)
    if base.scheme not in ('http', 'https') or base.username or base.password or base.query or base.fragment:
        parser.error('Base URL must be an HTTP(S) origin without credentials or query')
    cases = [json.loads(line) for line in args.cases.read_text(encoding='utf-8').splitlines() if line.strip()]
    ids = set()
    for case in cases:
        if not case.get('id') or case['id'] in ids or not case.get('query') or not isinstance(case.get('relevant_document_ids'), list):
            parser.error('Each case needs a unique id, query and explicitly labeled relevant_document_ids')
        ids.add(case['id'])
    if not cases:
        parser.error('No labeled cases supplied')
    results = evaluate(args.base_url, args.workspace, cases, cookie)
    report = {'revision': args.revision, 'workspace_id': args.workspace, 'contract': 'chat-go-v1',
              'knowledge_mode': 'live', 'summary': summarize(results), 'results': results}
    args.output.write_text(json.dumps(report, ensure_ascii=False, indent=2), encoding='utf-8')
    print(json.dumps(report['summary'], ensure_ascii=False))
    return 1 if report['summary']['failed'] or report['summary']['partial'] or report['summary']['forbidden_source_cases'] else 0


if __name__ == '__main__':
    raise SystemExit(main())
