#!/usr/bin/env bash
# subset-material-symbols.sh — 重新生成自托管 Material Symbols 子集字体（P1.5）。
#
# 用法：新增图标（模板里写了新的 material-symbols-outlined 图标名）后执行
#   bash scripts/subset-material-symbols.sh
# 产物：src/assets/fonts/material-symbols-outlined.woff2（约 4KB）
#
# 流程：npm 包 variable font(3.8MB) → 固定轴值实例化(376KB) → 全量 ligature
# 子集(244KB，闭合把所有 [a-z_] 组成的图标名都拉进来) → GSUB 过滤只留目标
# ligature → 二次子集(≈4KB)。需要 python3 + fontTools + brotli（自动建 venv）。
set -euo pipefail
cd "$(dirname "$0")/.."

FONT_SRC="node_modules/material-symbols/material-symbols-outlined.woff2"
OUT="src/assets/fonts/material-symbols-outlined.woff2"
[ -f "$FONT_SRC" ] || { echo "缺少 $FONT_SRC（npm install 后重试）"; exit 1; }
mkdir -p src/assets/fonts

# 图标清单 = 源码里实际使用的图标名（模板静态文本 + 动态绑定字面量）
# + EXTRA_ICONS（本次迭代新增、尚未落进模板扫描范围的名字）。
grep -rhoE "material-symbols-outlined[^>]*>\s*[a-z_]+" src --include='*.vue' \
  | grep -oE '[a-z_]+\s*$' | tr -d ' ' | sort -u > /tmp/ms-icons.txt
grep -rn "material-symbols-outlined" src --include='*.vue' -A2 \
  | grep -oE "'[a-z_]+'|>[a-z_]+<" | tr -d "'><" | sort -u >> /tmp/ms-icons.txt
EXTRA_ICONS='bolt fast_forward help more_vert notifications_active play_arrow progress_activity science subject'
printf '%s\n' $EXTRA_ICONS >> /tmp/ms-icons.txt
sort -u /tmp/ms-icons.txt -o /tmp/ms-icons.txt
echo "图标清单（$(wc -l < /tmp/ms-icons.txt | tr -d ' ') 个）："; cat /tmp/ms-icons.txt

VENV=/tmp/fonttools-venv
if [ ! -x "$VENV/bin/pyftsubset" ]; then
  python3 -m venv "$VENV"
  "$VENV/bin/pip" install -q fonttools brotli
fi

# 1) 固定轴值实例化（gvar 变量数据是体积大头）
"$VENV/bin/python" -m fontTools.varLib.instancer "$FONT_SRC" wght=400 FILL=0 GRAD=0 opsz=24 -o /tmp/ms-static.woff2 >/dev/null
# 2) 全量子集（保留 letters + 全部 ligature，供下一步过滤）
"$VENV/bin/pyftsubset" /tmp/ms-static.woff2 --text-file=/tmp/ms-icons.txt --layout-features='*' --flavor=woff2 --output-file=/tmp/ms-sub-star.woff2
# 3) GSUB 只保留目标 ligature（下划线字形名为 'underscore'，拼接时映射回 '_'）
"$VENV/bin/python" - <<'EOF'
from fontTools.ttLib import TTFont
names = set(l.strip() for l in open('/tmp/ms-icons.txt') if l.strip())
def lig_name(first, comps):
    return ''.join('_' if c == 'underscore' else c for c in [first] + list(comps))
f = TTFont('/tmp/ms-sub-star.woff2')
for lookup in f['GSUB'].table.LookupList.Lookup:
    for st in list(lookup.SubTable):
        ext = getattr(st, 'ExtSubTable', None)
        t = ext if ext is not None else st
        if hasattr(t, 'ligatures') and t.ligatures:
            new = {}
            for first, ligs in t.ligatures.items():
                keep = [l for l in ligs if lig_name(first, l.Component) in names]
                if keep:
                    new[first] = keep
            t.ligatures = new
f.flavor = 'woff2'
f.save('/tmp/ms-filtered.woff2')
EOF
# 4) 二次子集：closure 只能到达目标 ligature，得到最小字体
"$VENV/bin/pyftsubset" /tmp/ms-filtered.woff2 --text-file=/tmp/ms-icons.txt --layout-features='*' --flavor=woff2 --output-file="$OUT"

echo "产物：$OUT ($(du -h "$OUT" | cut -f1))"
