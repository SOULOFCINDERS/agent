#!/usr/bin/env python3
"""
DeepEval Comparison Test Script

Usage:
    pip install deepeval
    export OPENAI_API_KEY=your_key
    python run_deepeval.py --input deepeval_input.json --output deepeval_results.json
"""
import argparse, json, sys, os
from collections import defaultdict

def main():
    parser = argparse.ArgumentParser(description="Run DeepEval metrics")
    parser.add_argument("--input", required=True)
    parser.add_argument("--output", required=True)
    parser.add_argument("--metrics", default="faithfulness,hallucination,answer_relevancy")
    parser.add_argument("--threshold", type=float, default=0.5)
    parser.add_argument("--model", default=None)
    parser.add_argument("--base-url", default=None)
    args = parser.parse_args()

    try:
        from deepeval.test_case import LLMTestCase
        from deepeval.metrics import FaithfulnessMetric, HallucinationMetric, AnswerRelevancyMetric
    except ImportError:
        print("ERROR: DeepEval not installed. Run: pip install deepeval")
        sys.exit(1)

    if args.base_url:
        os.environ["DEEPEVAL_BASE_URL"] = args.base_url
    if args.model:
        os.environ["DEEPEVAL_MODEL"] = args.model

    with open(args.input, "r", encoding="utf-8") as f:
        data = json.load(f)

    test_cases_raw = data.get("test_cases", [])
    print(f"Loaded {len(test_cases_raw)} test cases")
    metrics_to_run = [m.strip() for m in args.metrics.split(",")]
    all_results = []

    metric_map = {
        "faithfulness": FaithfulnessMetric,
        "hallucination": HallucinationMetric,
        "answer_relevancy": AnswerRelevancyMetric,
    }

    for tc_raw in test_cases_raw:
        tc_id = tc_raw.get("id", "unknown")
        actual_output = tc_raw.get("actual_output", "")
        input_text = tc_raw.get("input", "")
        context = tc_raw.get("context", [])
        retrieval_context = tc_raw.get("retrieval_context", context)

        if not actual_output or actual_output == "[no reply captured]":
            continue

        test_case = LLMTestCase(
            input=input_text,
            actual_output=actual_output,
            context=context if context else None,
            retrieval_context=retrieval_context if retrieval_context else None,
        )

        for metric_name in metrics_to_run:
            cls = metric_map.get(metric_name)
            if cls is None:
                continue
            try:
                metric = cls(threshold=args.threshold)
                metric.measure(test_case)
                result = {
                    "id": tc_id, "metric": metric_name,
                    "score": metric.score,
                    "reason": getattr(metric, "reason", ""),
                    "passed": metric.is_successful(),
                }
                all_results.append(result)
                status = "PASS" if result["passed"] else "FAIL"
                print(f"  [{status}] {tc_id} | {metric_name}: {metric.score:.2f}")
            except Exception as e:
                print(f"  [ERR] {tc_id} | {metric_name}: {e}")
                all_results.append({"id": tc_id, "metric": metric_name, "score": 0.0, "reason": str(e), "passed": False})

    with open(args.output, "w", encoding="utf-8") as f:
        json.dump(all_results, f, ensure_ascii=False, indent=2)

    print(f"Results written to: {args.output}")
    _print_summary(all_results)

def _print_summary(results):
    stats = defaultdict(lambda: {"total": 0, "passed": 0, "score_sum": 0.0})
    for r in results:
        m = r["metric"]
        stats[m]["total"] += 1
        if r["passed"]: stats[m]["passed"] += 1
        stats[m]["score_sum"] += r["score"]
    print()
    print("=" * 55)
    print("  DeepEval Summary")
    print("=" * 55)
    for metric, s in sorted(stats.items()):
        pr = s["passed"] / s["total"] * 100 if s["total"] > 0 else 0
        avg = s["score_sum"] / s["total"] if s["total"] > 0 else 0
        print(f"  {metric:<20} pass={pr:.1f}%  avg={avg:.2f}")
    print("=" * 55)

if __name__ == "__main__":
    main()
