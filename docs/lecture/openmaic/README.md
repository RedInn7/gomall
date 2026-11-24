# 18 红包 · OpenMAIC 课堂包

`18-redpacket.maic.zip` 是讲义 `../18-redpacket.md` 的课堂包，可在 [OpenMAIC](https://github.com/THU-MAIC/OpenMAIC) 里直接导入播放。
共 44 个场景：40 页幻灯片（时序图、流程图都是可逐个高亮的形状和箭头）、3 组随堂测验、1 个互动实验。

## 导入
1. 本地运行 OpenMAIC：需要 Node ≥ 22.19、pnpm、PostgreSQL，在 OpenMAIC 目录执行 `pnpm db:up`，再 `pnpm dev`。
2. 打开首页，点“导入课堂”，选 `18-redpacket.maic.zip`。

## 不依赖大模型的功能

- 幻灯片翻页与播放：每页的聚光灯 / 激光笔和讲稿字幕都已写在包里。
- 随堂测验：全部是单选 / 多选，自带答案和解析，本地判分。
- 互动实验：一份自包含网页，没有外部依赖和网络请求。

包里没有音频；朗读、课堂问答要另配语音或模型服务，不配也能看完全部内容。

## 修改内容
改讲义或 `build_manifest.py` 后执行 `python3 build_manifest.py`（只用标准库），重新生成清单和 zip 并自检。`18-redpacket.manifest.json` 与 zip 里的 `manifest.json` 相同，方便看 diff。
