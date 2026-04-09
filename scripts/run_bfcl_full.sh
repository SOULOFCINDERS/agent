#!/bin/bash
# =========================================
# BFCL 完整评测脚本
# 用法: bash scripts/run_bfcl_full.sh [max_cases]
# 
# 参数:
#   max_cases  最多评测条数 (0=全部, 默认=0)
#
# 输出:
#   bfcl_reports/<timestamp>/  包含所有报告
# =========================================
set -euo pipefail
cd "$(dirname "$0")/.."

# 加载 .env
if [ -f .env ]; then
    export $(grep -v '^#' .env | grep -v '^$' | xargs)
    echo ".env loaded"
else
    echo ".env not found"
    exit 1
fi

MAX=${1:-0}
TIMESTAMP=$(date +%Y%m%d_%H%M%S)
REPORT_DIR="bfcl_reports/${TIMESTAMP}"
mkdir -p "${REPORT_DIR}"

echo ""
echo "BFCL Full Evaluation Suite"
echo "Model: ${LLM_MODEL:-unknown}"
echo "URL:   ${LLM_BASE_URL:-unknown}"
echo "Max:   ${MAX} (0=all)"
echo ""

# --- 1. exec_simple (100 cases) ---
echo "Phase 1/2: BFCL exec_simple (100 cases)"
go run ./cmd/bfcl/ \
    -file testdata/bfcl/BFCL_v3_exec_simple.json \
    -max "${MAX}" \
    -v=true \
    -json "${REPORT_DIR}/exec_simple.json" \
    2>&1 | tee "${REPORT_DIR}/exec_simple.log"

echo ""

# --- 2. simple (399 cases) ---
echo "Phase 2/2: BFCL simple (399 cases)"
go run ./cmd/bfcl/ \
    -file testdata/bfcl/BFCL_v3_simple.json \
    -max "${MAX}" \
    -v=true \
    -json "${REPORT_DIR}/simple.json" \
    2>&1 | tee "${REPORT_DIR}/simple.log"

echo ""
echo "EVALUATION COMPLETE"
echo "Reports: ${REPORT_DIR}/"
