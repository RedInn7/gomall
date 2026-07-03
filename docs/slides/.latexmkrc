# metropolis 进度条在首遍（无 .aux）会 Arithmetic overflow 中断；
# force 模式让 latexmk 首遍报错继续跑，第二遍有 .aux 后即正常。
# 真错误最终仍以非零退出码暴露，不会被吞掉。
$force_mode = 1;
