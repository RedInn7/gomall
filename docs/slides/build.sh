#!/usr/bin/env bash
# docs/slides/build.sh —— 一键编译全部 5 份 Beamer deck
# 用法：
#   ./build.sh           编译全部 5 份 deck
#   ./build.sh 01        只编译 01 开头的 deck
#   ./build.sh --master  编译全部 + 拼接 master.pdf（需 pdfunite）
#   ./build.sh --clean   只清理中间产物，不编译
#
# 行为：
#   - 每份 deck 跑 xelatex 两遍（解决 TOC / appendixnumberbeamer 引用）
#   - 任一份失败立即退出
#   - 清理 .aux / .log / .toc / .nav / .snm / .out / .vrb 中间产物
#   - 输出每份 PDF 的页数（要求落在 [20, 30]）

set -euo pipefail

# 确保 bash 把脚本里的 UTF-8 中文按多字节字符解析（避免 set -u 把
# `$pages（` 这类紧贴的全角字符当成变量名的一部分）。
export LC_ALL="${LC_ALL:-en_US.UTF-8}"

# ----------- 路径 & 颜色 -----------
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

if [[ -t 1 ]]; then
  C_OK=$'\033[32m'; C_WARN=$'\033[33m'; C_BAD=$'\033[31m'
  C_DIM=$'\033[2m';  C_BOLD=$'\033[1m';  C_RST=$'\033[0m'
else
  C_OK=""; C_WARN=""; C_BAD=""; C_DIM=""; C_BOLD=""; C_RST=""
fi

info()  { printf "%s[i]%s %s\n" "$C_DIM"  "$C_RST" "$*"; }
ok()    { printf "%s[ok]%s %s\n" "$C_OK"   "$C_RST" "$*"; }
warn()  { printf "%s[!]%s %s\n"  "$C_WARN" "$C_RST" "$*"; }
fail()  { printf "%s[x]%s %s\n"  "$C_BAD"  "$C_RST" "$*" >&2; exit 1; }
bold()  { printf "%s%s%s\n"      "$C_BOLD" "$*"          "$C_RST"; }

AUX_EXTS=(aux log toc nav snm out vrb fls fdb_latexmk)

clean_aux() {
  local base="$1"
  for ext in "${AUX_EXTS[@]}"; do
    rm -f "${base}.${ext}"
  done
}

clean_all_aux() {
  info "清理中间产物 (.aux .log .toc .nav .snm .out .vrb ...)"
  for f in 0?-*.tex; do
    [[ -f "$f" ]] || continue
    clean_aux "${f%.tex}"
  done
  # preamble 没有自己的 .aux，但保险起见
  clean_aux preamble
}

# ----------- 参数解析 -----------
FILTER=""
BUILD_MASTER=0
if [[ "${1:-}" == "--clean" ]]; then
  clean_all_aux
  ok "清理完成"
  exit 0
elif [[ "${1:-}" == "--master" ]]; then
  BUILD_MASTER=1
elif [[ -n "${1:-}" ]]; then
  FILTER="$1"
fi

# ----------- 前置检查 -----------
command -v xelatex >/dev/null 2>&1 || fail "找不到 xelatex，请先安装 TeX Live (Linux) / MacTeX (macOS)。"
command -v pdfinfo >/dev/null 2>&1 || warn "找不到 pdfinfo（poppler-utils），将跳过页数检查。"

bold "==> gomall slides build"
info "工作目录: $SCRIPT_DIR"
info "xelatex:  $(xelatex --version | head -n1)"
[[ -n "$FILTER" ]] && info "过滤器:   $FILTER*.tex"
echo

# ----------- 主循环 -----------
declare -a BUILT_PDFS=()
FAILED=0

shopt -s nullglob
# 注意：合订本封面 00-master-cover.tex 是辅助产物，仅 3 页，不参与 [20, 30]
# 页数校验；它会在 master.pdf 拼接时单独编译（见 build.sh --master）。
for src in 0?-*.tex; do
  base="${src%.tex}"
  if [[ -n "$FILTER" && "$base" != ${FILTER}* ]]; then
    continue
  fi
  if [[ "$base" == "00-master-cover" ]]; then
    continue
  fi

  bold "[*] 编译 $src"

  # 第一遍：建立 .aux / .toc
  if ! xelatex -interaction=nonstopmode -halt-on-error "$src" \
       >"${base}.build.log" 2>&1; then
    fail "$src 第一遍 xelatex 失败，详情见 ${base}.build.log"
  fi

  # 第二遍：解 TOC / appendixnumberbeamer 引用
  if ! xelatex -interaction=nonstopmode -halt-on-error "$src" \
       >"${base}.build.log" 2>&1; then
    fail "$src 第二遍 xelatex 失败，详情见 ${base}.build.log"
  fi

  pdf="${base}.pdf"
  if [[ ! -f "$pdf" ]]; then
    fail "$src 编译后未生成 $pdf"
  fi

  # 页数检查
  pages=""
  if command -v pdfinfo >/dev/null 2>&1; then
    pages=$(pdfinfo "$pdf" 2>/dev/null | awk '/^Pages:/ {print $2}')
    if [[ -n "$pages" ]]; then
      if (( pages < 20 || pages > 30 )); then
        warn "$pdf 页数 = ${pages}, 超出预期范围 [20, 30]"
      else
        ok "$pdf 页数 = ${pages} (OK, 在 [20, 30] 范围内)"
      fi
    fi
  else
    ok "$pdf 已生成"
  fi

  BUILT_PDFS+=("$pdf")
  rm -f "${base}.build.log"
  echo
done

# ----------- 收尾 -----------
clean_all_aux

if (( ${#BUILT_PDFS[@]} == 0 )); then
  fail "没有匹配到任何源文件"
fi

bold "==> 编译完成，共 ${#BUILT_PDFS[@]} 份 PDF"
for p in "${BUILT_PDFS[@]}"; do
  size=$(du -h "$p" | awk '{print $1}')
  printf "    %s  (%s)\n" "$p" "$size"
done
echo

# ----------- 可选：拼接 master.pdf -----------
if (( BUILD_MASTER )); then
  bold "[*] 拼接合订本 master.pdf"
  if ! command -v pdfunite >/dev/null 2>&1; then
    fail "找不到 pdfunite（poppler-utils），无法拼接 master.pdf。"
  fi

  cover_src="00-master-cover.tex"
  cover_pdf="00-master-cover.pdf"
  if [[ ! -f "$cover_src" ]]; then
    fail "找不到合订本封面 $cover_src"
  fi
  info "编译合订本封面 $cover_src"
  if ! xelatex -interaction=nonstopmode -halt-on-error "$cover_src" \
       >"${cover_src%.tex}.build.log" 2>&1; then
    fail "$cover_src 第一遍编译失败"
  fi
  if ! xelatex -interaction=nonstopmode -halt-on-error "$cover_src" \
       >"${cover_src%.tex}.build.log" 2>&1; then
    fail "$cover_src 第二遍编译失败"
  fi
  rm -f "${cover_src%.tex}.build.log"

  pdfunite "$cover_pdf" \
           01-idempotency.pdf \
           02-anti-oversell.pdf \
           03-cache-consistency.pdf \
           04-outbox-saga.pdf \
           05-ratelimit-circuit-breaker.pdf \
           master.pdf

  if command -v pdfinfo >/dev/null 2>&1; then
    mpages=$(pdfinfo master.pdf | awk '/^Pages:/ {print $2}')
    msize=$(du -h master.pdf | awk '{print $1}')
    ok "master.pdf 已生成: ${mpages} 页, ${msize}"
  else
    ok "master.pdf 已生成"
  fi
  clean_aux 00-master-cover
fi

ok "全部成功。"
