# 编译成功后自动清中间产物，目录只留 .tex 和 .pdf。
# .synctex.gz 例外保留：VS Code PDF<->源码双向跳转靠它。
# 编译失败不触发清理，.log 留下供排错。
# 代价：删 .aux/.fdb_latexmk 后每次编译都是冷启动固定两遍，保存后重编慢一点是预期行为。
# （metropolis 冷启动 Arithmetic overflow 已在 preamble.tex 里根治：\appendixtotalframenumber 默认置 1。）
$success_cmd = 'rm -f %R.aux %R.log %R.nav %R.out %R.snm %R.toc %R.vrb %R.fls %R.fdb_latexmk %R.xdv %R.bbl %R.blg';
