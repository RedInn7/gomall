#!/usr/bin/env python3
"""把 ../18-redpacket.md 做成 OpenMAIC 课堂包（.maic.zip）。只用标准库。

用法：python3 build_manifest.py
产出：18-redpacket.manifest.json、18-redpacket.maic.zip（根目录只有 manifest.json）。

版面在本文件里手工排；讲稿原文不在这里重抄，而是从讲义里读出来，
每个 cue 只写“这一句从哪几个字开始”，脚本按这些起点把讲稿切成 speech。
讲义改了讲稿，起点对不上时构建会直接报错。
"""
import html
import json
import math
import os
import re
import zipfile

HERE = os.path.dirname(os.path.abspath(__file__))
SRC_MD = os.path.join(HERE, '..', '18-redpacket.md')
OUT_JSON = os.path.join(HERE, '18-redpacket.manifest.json')
OUT_ZIP = os.path.join(HERE, '18-redpacket.maic.zip')

W, H = 1000, 562.5
BG, O1, O2 = '#FFF8EE', '#E8590C', '#D9480F'
F1, F2 = '#FFE8CC', '#FFD8A8'
NOTE, NOTE_B = '#FFF3BF', '#F08C00'
RED, RED_BG = '#C92A2A', '#FFE3E3'
INK = '#3D1F0B'
FONT = 'Microsoft YaHei'
MONO = 'Menlo,Consolas,monospace'
SANS = 'PingFang SC,Microsoft YaHei,Hiragino Sans GB,sans-serif'
# 固定时间戳：重新生成时 diff 里只出现内容变化
EXPORTED_AT = '2026-11-23T00:00:00.000Z'
CREATED_AT = 1795392000000

SCRIPTS = [m.group(1).strip() for m in
           re.finditer(r'^> \*\*讲稿\*\*[\s　]*(.+)$', open(SRC_MD, encoding='utf-8').read(), re.M)]
assert len(SCRIPTS) == 34, f'讲义里应有 34 段讲稿，实际 {len(SCRIPTS)}'

SCENES = []   # Slide 对象或现成的 scene dict，按播放顺序
SLIDES = []


def plain(t):
    return t.replace('**', '').replace('`', '')


def tw(t, fs, bold=False):
    """估算一行文字的像素宽度：全角按 fs，大写字母 0.72 fs，其余半角 0.57 fs。"""
    w = sum(fs if ord(c) > 0x2000 else fs * (0.72 if c.isupper() else 0.57) for c in plain(t))
    return w * (1.05 if bold else 1)


def nlines(t, fs, inner, bold=False):
    return sum(max(1, math.ceil(tw(seg, fs, bold) * 1.04 / inner)) for seg in t.split('\n'))


def fmt(t):
    """极简标记：**粗体**、`等宽`。"""
    t = html.escape(t, quote=False)
    t = re.sub(r'\*\*(.+?)\*\*', r'<strong>\1</strong>', t)
    return re.sub(r'`(.+?)`', rf'<span style="font-family:{MONO};color:{O2};">\1</span>', t)


def para(t, fs, color, align, bold=False, mono=False, lh=1.5):
    st = f'font-size:{fs}px;color:{color};text-align:{align};line-height:{lh};'
    st += 'font-weight:bold;' if bold else ''
    st += f'font-family:{MONO};' if mono else f'font-family:{SANS};'
    return ''.join(f'<p style="{st}">{fmt(seg)}</p>' for seg in t.split('\n'))


def shape_path(kind, w, h, r=8):
    if kind == 'pill':
        r = h / 2
    if kind == 'hex':
        c = 12
        return f'M {c} 0 H {w - c} L {w} {h / 2} L {w - c} {h} H {c} L 0 {h / 2} Z'
    if kind == 'dot':
        return f'M 0 {h / 2} A {w / 2} {h / 2} 0 1 0 {w} {h / 2} A {w / 2} {h / 2} 0 1 0 0 {h / 2} Z'
    r = min(r, w / 2, h / 2)
    return (f'M {r} 0 H {w - r} Q {w} 0 {w} {r} V {h - r} Q {w} {h} {w - r} {h} '
            f'H {r} Q 0 {h} 0 {h - r} V {r} Q 0 0 {r} 0 Z')


class Slide:
    def __init__(s, title, src=None, title_fs=28):
        s.n = len(SLIDES) + 1
        s.title, s.src, s.els, s.cues = title, src, [], []
        SLIDES.append(s)
        SCENES.append(s)
        s.tid = s.T(40, 10, 920, title, fs=title_fs, bold=True, color=O2, lines=1)
        s.B(40, 66, 64, 4, fill=O1, stroke=O1, r=2)

    def _add(s, el):
        el = {'id': f's{s.n:02d}_e{len(s.els) + 1}', **el}
        s.els.append(el)
        return el['id']

    # ---- 文本 ----
    def T(s, x, y, w, t, fs=16, color=INK, bold=False, align='left', lines=None, fill=None, mono=False):
        if lines is None:
            lines = nlines(t, fs, w - 20, bold)
        el = dict(type='text', left=round(x, 1), top=round(y, 1), width=round(w, 1),
                  height=round(lines * fs * 1.5 + 20, 1), rotate=0,
                  content=para(t, fs, color, align, bold, mono),
                  defaultFontName=FONT, defaultColor=color, lineHeight=1.5)
        if fill:
            el['fill'] = fill
        return s._add(el)

    # ---- 形状（可带文字）----
    def B(s, x, y, w, h=None, t='', fill=F1, stroke=O1, fs=14, color=INK, bold=False,
          kind='round', r=8, sw=1.5, align='center'):
        if h is None:
            h = nlines(t, fs, w - 22, bold) * fs * 1.35 + 20
        el = dict(type='shape', left=round(x, 1), top=round(y, 1), width=round(w, 1), height=round(h, 1),
                  rotate=0, viewBox=[round(w, 1), round(h, 1)], path=shape_path(kind, round(w, 1), round(h, 1), r),
                  fixedRatio=False, fill=fill, outline=dict(style='solid', width=sw, color=stroke))
        if t:
            el['text'] = dict(content=para(t, fs, color, align, bold, lh=1.35), defaultFontName=FONT,
                              defaultColor=color, align='middle', lineHeight=1.35)
        return s._add(el)

    # ---- 线 / 箭头 ----
    def L(s, x1, y1, x2, y2, color=O2, style='solid', arrow=True, w=2, via=None, both=False):
        xs, ys = [x1, x2] + ([via[0]] if via else []), [y1, y2] + ([via[1]] if via else [])
        l, t = min(xs), min(ys)
        el = dict(type='line', left=round(l, 1), top=round(t, 1), width=w,
                  start=[round(x1 - l, 1), round(y1 - t, 1)], end=[round(x2 - l, 1), round(y2 - t, 1)],
                  style=style, color=color, points=['arrow' if both else '', 'arrow' if arrow else ''])
        if via:
            el['broken'] = [round(via[0] - l, 1), round(via[1] - t, 1)]
        return s._add(el)

    def A(s, x1, y1, x2, y2, label=None, lp='above', **kw):
        """箭头 + 可选标签。lp：above / right / below。"""
        lid = s.L(x1, y1, x2, y2, **kw)
        if not label:
            return lid
        mx, my, w = (x1 + x2) / 2, (y1 + y2) / 2, tw(label, 14) * 1.04 + 24
        if lp == 'right':
            return s.T(mx + 2, my - 20.5, w, label, fs=14, lines=1)
        return s.T(mx - w / 2, my - 33 if lp == 'above' else my - 4, w, label, fs=14, align='center', lines=1)

    def bullets(s, items, x, y, w, fs=16, gap=8):
        ids = []
        for it in items:
            n = nlines('• ' + it, fs, w - 20)
            ids.append(s.T(x, y, w, '• ' + it, fs=fs, lines=n))
            y += n * fs * 1.5 + gap
        return ids

    def code(s, x, y, w, lang, src, name=None, numbers=True):
        lines = src.strip('\n').split('\n')
        el = dict(type='code', left=x, top=y, width=w, height=len(lines) * 23 + 48, rotate=0, language=lang,
                  lines=[dict(id=f'L{i + 1}', content=ln) for i, ln in enumerate(lines)],
                  showLineNumbers=numbers, fontSize=14)
        if name:
            el['fileName'] = name
        return s._add(el)

    def grid(s, x, y, cols, rows, rowh, header=None, fs=14, gap=2, fills=None):
        """表格：每行一个底色条（可被高亮）+ 每格一个 text。返回 (表头条, 行条列表, 单元格 id 矩阵)。"""
        hdr, strips, cells = None, [], []
        for r, row in enumerate(([header] if header else []) + rows):
            is_h = bool(header) and r == 0
            i = r - (1 if header else 0)
            h = 32 if is_h else rowh
            sid = s.B(x, y, sum(cols), h, fill=F2 if is_h else (fills[i] if fills else F1),
                      stroke='#F5C99B', r=4, sw=1)
            cx, ids = x, []
            for cw, cell in zip(cols, row):
                n = nlines(cell, fs, cw - 20, is_h)
                ids.append(s.T(cx, y + (h - (n * fs * 1.5 + 20)) / 2, cw, cell, fs=fs, bold=is_h, lines=n))
                cx += cw
            if is_h:
                hdr = sid
            else:
                strips.append(sid)
                cells.append(ids)
            y += h + gap
        return hdr, strips, cells

    def vstack(s, x, y, w, items, gap=16, fs=14):
        """竖排一串方框，中间用向下箭头连起来。items: 文字 或 (文字, 'red')。"""
        ids = []
        for i, it in enumerate(items):
            t, red = (it, False) if isinstance(it, str) else (it[0], True)
            h = nlines(t, fs, w - 22) * fs * 1.35 + 20
            if i:
                s.L(x + w / 2, y - gap, x + w / 2, y, color=RED if red else O2, style='dashed' if red else 'solid')
            ids.append(s.B(x, y, w, h, t, fs=fs, fill=RED_BG if red else F1, stroke=RED if red else O1,
                           align='left'))
            y += h + gap
        return ids

    # ---- 时序图 ----
    def seq(s, parts, steps, top, bottom):
        """steps: ('m', 从, 到, 文字[, 'r' 返回虚线 | 'x' 失败红线 | 'b' 双向])
                  ('n', 从, 到, 备注文字[, True 表示红色])
                  ('s', 谁, 自调用文字)
        返回 (参与者 id 列表, 每一步可高亮的 id 列表)。"""
        n = len(parts)
        lo, hi = {2: (260, 740), 3: (150, 850), 4: (115, 885), 5: (100, 900)}[n]
        cx = [lo + (hi - lo) * i / (n - 1) for i in range(n)]
        ph = 52 if any('\n' in p for p in parts) else 36
        pw = min(200, cx[1] - cx[0] - 14)
        pids = [s.B(c - pw / 2, top, pw, ph, p, fill=F2, bold=True) for c, p in zip(cx, parts)]
        y0 = top + ph
        for c in cx:
            s.L(c, y0, c, bottom, color=NOTE_B, style='dashed', arrow=False, w=1)
        unit = (bottom - y0 - 8) / len(steps)
        assert unit >= 33, f'第 {s.n} 页时序图太挤：每步只有 {unit:.1f}px'
        ids, arrows, boxes = {}, [], []
        for i, st in enumerate(steps):
            yb = y0 + 6 + (i + 1) * unit
            if st[0] == 'm':
                _, a, b, text, *k = st
                k = k[0] if k else ''
                ya = yb - 5
                w = tw(text, 14) * 1.04 + 24
                x = min(max((cx[a] + cx[b]) / 2 - w / 2, 4), 996 - w)
                crossed = any(x + 8 < c < x + w - 8 for c in cx)   # 压到生命线就垫一层底色
                ids[i] = s.T(x, ya - 32, w, text, fs=14, color=RED if k == 'x' else INK, align='center',
                             lines=1, fill=BG if crossed else None)
                arrows.append((cx[a], ya, cx[b], ya, RED if k == 'x' else O2,
                               'dashed' if k == 'r' else 'solid', k == 'b'))
            else:
                if st[0] == 'n':
                    _, a, b, text, *d = st
                    red = bool(d and d[0])
                    fill, stroke, col = (RED_BG, RED, RED) if red else (NOTE, NOTE_B, INK)
                else:
                    _, a, text = st
                    b, fill, stroke, col = a, F1, O1, INK
                x0, x1 = min(cx[a], cx[b]) - 60, max(cx[a], cx[b]) + 60
                need = tw(text, 14) * 1.04 + 28
                if need > x1 - x0:
                    m = (x0 + x1) / 2
                    x0, x1 = m - need / 2, m + need / 2
                shift = max(8 - x0, 0) - max(x1 - 992, 0)
                boxes.append((i, x0 + shift, yb - unit / 2 - 15, x1 - x0, text, fill, stroke, col))
        for x1, y1, x2, y2, col, style, both in arrows:
            s.L(x1, y1, x2, y2, color=col, style=style, both=both)
        for i, x, y, w, text, fill, stroke, col in boxes:
            ids[i] = s.B(x, y, w, 30, text, fill=fill, stroke=stroke, color=col)
        return pids, [ids[i] for i in range(len(steps))]

    # ---- 讲稿 cue ----
    def cue(s, target, start='', laser=False):
        """start：这一句在讲稿里开头的几个字；空串表示从这段讲稿的开头起。"""
        s.cues.append((target, start, laser))

    def scene(s, order, speeches):
        actions = []
        for (target, _, laser), text in zip(s.cues, speeches):
            actions.append(dict(id=f's{s.n:02d}_a{len(actions) + 1}', type='laser' if laser else 'spotlight',
                                elementId=target))
            actions.append(dict(id=f's{s.n:02d}_a{len(actions) + 1}', type='speech', text=text))
        canvas = dict(id=f'slide-{s.n:02d}', viewportSize=W, viewportRatio=H / W,
                      theme=dict(backgroundColor=BG, themeColors=[O1, O2, NOTE_B, RED], fontColor=INK, fontName=FONT),
                      elements=s.els, background=dict(type='solid', color=BG))
        return dict(type='slide', title=s.title, order=order,
                    content=dict(type='slide', canvas=canvas), actions=actions)


def quiz(title, qs):
    questions = []
    for i, (kind, q, opts, ans, why) in enumerate(qs):
        questions.append(dict(id=f'q{len(SCENES)}_{i + 1}', type=kind, question=q,
                              options=[dict(label=o, value='ABCD'[j]) for j, o in enumerate(opts)],
                              answer=list(ans), analysis=why, hasAnswer=True, points=1))
    SCENES.append(dict(type='quiz', title=title, content=dict(type='quiz', questions=questions)))


# =====================================================================
# 封面
# =====================================================================
s = Slide('18 红包：Redis 分发与 DB 记账的一致性', src=0, title_fs=32)
s.T(40, 74, 920, '模块定位：`gomall` · `internal/redpacket`（发包、抢包、退款与对账） / '
    '`repository/cache/redpacket.go`（二倍均值拆包与 Lua 脚本）', fs=14)
_, rows, _ = s.grid(40, 138, [90, 610, 220], [
    ['00', '为什么要两套存储，谁说了算', ''],
    ['一', '四张客诉单与数据结构', 'A B C D'],
    ['二', '分发：发包时预拆，抢包只 `LPOP`', ''],
    ['三', '资金：钱先进托管账户', ''],
    ['四', '一致性：Redis 先扣，DB 说了算', 'A B'],
    ['五', '三道兜底防线', 'C D'],
    ['六', '故障演练、课后思考、代码索引', ''],
], 42, header=['部分', '讲什么', '对应工单'], fs=16)
s.cue(s.tid)
s.cue(rows[4], '我们用 Redis 管争抢')
s.cue(rows[1], '先看四张客诉单')

# =====================================================================
# 00 为什么要两套存储
# =====================================================================
s = Slide('00-1 只用 MySQL：所有人排同一把锁', src=1)
_, st = s.seq(['Client', 'Service', 'DB'], [
    ('n', 0, 0, '100 人红包，几百人同一毫秒点开'),
    ('m', 0, 1, '抢红包 (几百个并发请求)'),
    ('m', 1, 2, 'SELECT ... FOR UPDATE (同一行红包记录)'),
    ('n', 2, 2, '一个请求持锁：算随机金额、改余额、写流水'),
    ('n', 1, 2, '其余几百个请求全部排队等这把行锁'),
    ('m', 2, 1, '锁等待超时', 'x'),
    ('m', 1, 0, '失败 (数据库被打挂)', 'r'),
], 90, 520)
s.cue(s.tid)
s.cue(st[2], '大年三十群里')
s.cue(st[3], '拿到锁的那个人')
s.cue(st[5], '队一长')
s.cue(st[6], '所以只用 MySQL', laser=True)

s = Slide('00-2 只用 Redis：扣了份额，钱不一定到账', src=2)
_, st = s.seq(['Client', 'Service', 'Redis'], [
    ('m', 0, 1, '抢红包'),
    ('m', 1, 2, '扣一份份额 (LPOP)'),
    ('m', 2, 1, '(3000 分)', 'r'),
    ('n', 1, 1, '准备给用户钱包加钱时进程崩溃', True),
    ('m', 1, 0, '连接断开', 'x'),
    ('n', 2, 2, '份额已扣，钱没到账，没有跨表事务可以回滚'),
], 90, 500)
s.cue(st[1])
s.cue(st[5], '但 Redis 不是账本')
s.cue(st[3], '份额扣掉了')
s.cue(st[5], '所以只用 Redis', laser=True)

s = Slide('00-3 铁律：Redis 发号码牌，MySQL 发钱', src=3)
s.B(40, 96, 130, 70, '抢包请求', kind='pill')
rd = s.B(215, 96, 250, 70, '**Redis**\n发号码牌：谁拿到哪一份')
db = s.B(520, 96, 250, 70, '**MySQL**\n发钱记账：钱有没有动')
s.B(825, 96, 135, 70, '钱到账', kind='pill', fill=NOTE, stroke=NOTE_B)
s.A(170, 131, 215, 131)
s.A(465, 131, 520, 131, 'amount')
s.A(770, 131, 825, 131)
bad = s.B(215, 206, 250, 44, 'Redis 里的剩余金额', fill=RED_BG, stroke=RED, color=RED)
s.L(465, 228, 645, 166, color=RED, style='dashed', via=(645, 228))
s.T(494, 195, 122, '对账时绝不采信', fs=14, color=RED, align='center', lines=1)
eq = s.B(40, 276, 920, 66, '**资金守恒**：红包总金额 (Total) = 所有领取人落库金额之和 '
         'SUM(red_packet_claim.amount) + 过期退回发包人的金额 (Leftover)', fill=NOTE, stroke=NOTE_B, fs=16)
s.bullets(['公式里每一项都**只从 MySQL 算**。', '两边不一致，一律向 MySQL 对齐。'], 40, 356, 920)
s.cue(rd)
s.cue(db, 'MySQL 才负责')
s.cue(eq, '请大家记住')
s.cue(bad, '无论中间')

s = Slide('00-4 全景图：发红包、抢红包、退红包', src=4)
heads = []
for x, head, items in [
    (40, '阶段一：发红包 (Create)', [
        '1. 校验支付密码 + 二倍均值算法，在内存预拆好 N 份金额数组 (如 [30, 50, 20])',
        '2. 开启 DB 事务：写 RedPacket 主表 + 扣发包人钱包 + 记入 redpacket_escrow',
        '3. 事务提交后：RPUSH 金额数组进 Redis LIST',
        ('Redis 写入失败：当场立即调 refundRedPacketLeft，全额退回发包人钱包，状态置为 refunded', 'red')]),
    (355, '阶段二：抢红包 (Claim)', [
        '1. 执行 Redis Lua 抢包脚本：HEXISTS 去重 → LPOP 弹出金额 → HSET 记标记（返回 -1 / -2：已抢完 84004 / 已领过 84005）',
        '2. 开启 DB 事务 (claimTx)：INSERT claim + remaining-1 + 托管转个人钱包',
        '3. 提交报错/超时：先反查 DB GetClaim(id, uid)，确认到底落库没有',
        '4. 确认未落库：执行回补 Lua，检查无 :r 锁后 LPUSH 放回金额 + HDEL 删已领标记']),
    (670, '阶段三：后台双轮兜底', [
        '每 2 分钟孤儿对账 (RunRedPacketClaimReconcile)：扫描超过 1 分钟未落库的标记 → 加 :r 锁 → 自动调用 claimTx 补发到账',
        '每 5 分钟过期回收 (RunRedPacketExpireCheck)：先跑一遍对账补齐孤儿 → 再按 DB 算 Total - SUM(已领) → 退回发包人'])]:
    heads.append(s.B(x, 82, 290, 36, head, fill=F2, bold=True, fs=15))
    s.vstack(x, 134, 290, items)
s.A(330, 100, 355, 100)
s.A(645, 100, 670, 100, style='dashed')
s.cue(s.tid)
s.cue(heads[0], '第一段发红包')
s.cue(heads[1], '第二段抢红包')
s.cue(heads[2], '第三段是后台')
s.cue(s.tid, '后面每一节', laser=True)

# =====================================================================
# 一、四张客诉单与数据结构
# =====================================================================
s = Slide('一、先看四张客诉单', src=5)
_, rows, _ = s.grid(40, 78, [60, 335, 300, 225], [
    ['**A**', '点“抢”报错失败；再点提示“您已领取过该红包”，钱也没到账',
     'Redis 扣了份额、打了已领标记，DB 事务失败后没把标记和份额退回 Redis', '**4.1、4.5**：失败先查库 + Saga 原子回补'],
    ['**B**', '到期同一秒最后一份被抢走；抢包人拿到钱，发包人也收到这笔的过期退款，平台亏钱',
     '领取事务与过期退款事务并发，读了旧的 MVCC 快照，同一份钱发两次', '**4.3、4.4**：`SELECT ... FOR UPDATE` 置于事务首行'],
    ['**C**', '抢的时候转圈断开；半小时后份额没退回红包池，钱包也没收到钱（孤儿份额）',
     '抢完 Redis 后服务器断电重启，回补没跑成，份额卡在半空', '**5.2**：`claimed` 孤儿对账自动补领（`:r` 抢占锁）'],
    ['**D**', '发包人被扣了 100 元，群里所有人点开都显示“红包已抢完”',
     'DB 扣款事务提交成功，紧接着写 Redis 金额列表时 Redis 挂了', '**5.3**：发包失败立即逆向退款 + 份额池丢失检测提前退款'],
], 76, header=['工单', '用户看到的现象', '底层原因', '解决章节'])
common = s.B(40, 432, 920, 44, '共同点：全都出在 Redis 和 MySQL 的交界处，一边做了，另一边没做。',
             fill=NOTE, stroke=NOTE_B, fs=16)
s.cue(s.tid)
s.cue(rows[0], 'A：我点抢')
s.cue(rows[1], 'B：红包到期')
s.cue(rows[2], 'C：抢的时候')
s.cue(rows[3], 'D：发红包')
s.cue(common, '这四张单子')

s = Slide('1.1 Redis 侧：两个 Key，抢包前后怎么变', src=6)
_, keys, _ = s.grid(40, 78, [250, 70, 600], [
    ['`redpacket:{id}:amounts`', '`LIST`', '发包时一次性 `RPUSH` 预拆好的金额数组（分），抢包时 `LPOP` 弹出'],
    ['`redpacket:{id}:claimed`', '`HASH`', '谁抢了多少、什么时候抢的：`userID -> "金额:unix秒[:r]"`'],
], 36)
s.T(40, 152, 920, '例子：100 元拆 3 份（30、50、20 元）。`repository/cache/redpacket.go:16-22`', fs=14)


def panel(s, x, y, title, amounts, claimed):
    s.T(x, y, 240, title, fs=14, bold=True, align='center', lines=1)
    return (s.B(x, y + 36, 240, 50, 'amounts\n' + amounts),
            s.B(x, y + 92, 240, 50, 'claimed\n' + claimed, fill=NOTE, stroke=NOTE_B))


panel(s, 40, 262, '① 发包后', '3000 | 5000 | 2000', '（空）')
_, c1 = panel(s, 350, 262, '② 用户 101 抢到 3000', '5000 | 2000', '101 → 3000:1730680000')
_, c2a = panel(s, 720, 196, '③a DB 失败，回补', '3000 | 5000 | 2000', '（101 的标记已删）')
_, c2b = panel(s, 720, 350, '③b 成了孤儿，对账接手', '5000 | 2000', '101 → 3000:1730680000:r')
s.A(280, 350, 350, 350, 'claim Lua')
s.L(590, 330, 720, 280)
s.T(590, 262, 110, 'rollback Lua', fs=14, align='center', lines=1)
s.L(590, 372, 720, 430)
s.T(575, 430, 160, 'LockForRepair Lua', fs=14, align='center', lines=1)
s.cue(keys[0])
s.cue(keys[1], '第二个是 HASH')
s.cue(c1, '看图：发包后')
s.cue(c2a, '之后有两种走向')
s.cue(c2b, '或者这一份成了孤儿')
s.cue(c1, '这里有个问题', laser=True)

s = Slide('1.1 MySQL 侧：两张核心表', src=7)
t1 = s.B(40, 80, 520, 30, 'RED_PACKET', fill=F2, bold=True)
s.grid(40, 112, [70, 120, 330], [
    ['uint', 'ID', 'PK'], ['uint', 'UserID', '发包人 ID'], ['int64', 'Total', '红包总金额（分）'],
    ['int', 'Count', '红包总份数'], ['int', 'Remaining', '剩余未领份数，领取事务内递减'],
    ['time', 'ExpireAt', '过期时间（最长 7 天）'], ['uint', 'Status', '1 active / 3 finished / 4 refunded'],
    ['string', 'Greeting', '祝福语，size 128'], ['string', 'ShareCode', '16 位随机领取口令'],
], 28)
t2 = s.B(590, 80, 370, 30, 'RED_PACKET_CLAIM', fill=F2, bold=True)
s.grid(590, 112, [70, 120, 180], [
    ['uint', 'ID', 'PK'], ['uint', 'RedPacketID', 'uk_rp_user 第 1 列'],
    ['uint', 'UserID', 'uk_rp_user 第 2 列'], ['int64', 'Amount', '抢到的金额（分）'],
], 28)
bl = s.bullets(['`Status`：`1` 进行中、`3` 已抢完、`4` 已过期退款。',
                '唯一索引 `uk_rp_user (red_packet_id, user_id)`：DB 层兜底“同一用户只能领一次”。'],
               590, 244, 370, fs=15)
s.T(40, 392, 520, '`internal/redpacket/model.go:20-44`', fs=14)
s.T(40, 420, 520, 'RED_PACKET 1 —— 被领取 —— N RED_PACKET_CLAIM', fs=14)
s.cue(t1)
s.cue(t2, '领取表一个人领一次')
s.cue(bl[1], '注意领取表上的唯一索引')
s.cue(bl[1], '后面对账补领', laser=True)

s = Slide('1.2 防扫号口令 ShareCode', src=8)
_, st = s.seq(['Sender', 'Service', 'Friend', 'Attacker'], [
    ('m', 0, 1, 'POST 发红包'),
    ('m', 1, 0, '(id=88, ShareCode=a3f9c0128b7e4d56，只返回这一次)', 'r'),
    ('m', 0, 2, '分享链接 (id + ShareCode)'),
    ('m', 2, 1, '抢红包 (id=88, code)'),
    ('m', 1, 2, '口令正确，进入抢包', 'r'),
    ('m', 3, 1, '脚本遍历 id = 1, 2, 3 ... (没有口令)'),
    ('m', 1, 3, '84006 口令不对', 'r'),
], 90, 500)
s.cue(st[5])
s.cue(st[1], '所以发包时后端')
s.cue(st[6], '没有口令的请求')

s = Slide('1.2 口令比较：为什么用常量时间比较', src=8)
cd = s.code(40, 84, 920, 'go', '''
func codeMatches(rp *RedPacket, code string, legacyOK bool) bool {
    if rp.ShareCode == "" {
        return legacyOK
    }
    return subtle.ConstantTimeCompare([]byte(rp.ShareCode), []byte(code)) == 1
}
''', name='internal/redpacket/service.go:161-166')
bl = s.bullets(['普通 `==` 逐字节比较，第 1 位错就立刻返回，前 15 位都对才比第 16 位。',
                '攻击者测微秒级时间差，能一位一位猜出口令，这叫**计时攻击（Timing Attack）**。',
                '`ConstantTimeCompare` 永远比完所有字节，耗时恒定。'], 40, 300, 920)
s.cue(cd, '再看代码里一个细节')
s.cue(bl[0], '因为等号是')
s.cue(bl[1], '攻击者量这个时间差')
s.cue(bl[2], '常量时间比较不管')

# =====================================================================
# 二、分发模型
# =====================================================================
s = Slide('二、分发模型：发包时预拆好，抢包只做 LPOP', src=9)
_, st = s.seq(['Sender', 'Service', 'DB', 'Redis', 'Client'], [
    ('m', 0, 1, '发红包 (100 元, 3 份)'),
    ('s', 1, 'SplitRedPacket → [3000, 5000, 2000]'),
    ('m', 1, 2, '事务 (写主表 + 扣款进托管)'),
    ('m', 1, 3, 'RPUSH amounts (3000, 5000, 2000)'),
    ('m', 1, 0, '(id, ShareCode)', 'r'),
    ('n', 1, 4, 'loop：每个抢包请求'),
    ('m', 4, 1, '抢红包'),
    ('m', 1, 3, 'claim Lua (LPOP 一次)'),
    ('m', 3, 1, '(amount)', 'r'),
    ('n', 1, 3, '热路径上没有随机数计算，没有分布式锁'),
], 78, 506)
b = s.T(40, 510, 920, '• 抢 = `LPOP` 拿走一张固定面额的“代金券”；失败回补 = `LPUSH` 原样塞回一张。', fs=14, lines=1)
s.cue(s.tid)
s.cue(st[1], '发包是低频操作')
s.cue(st[7], '抢包是高并发热路径')
s.cue(st[9], '有同学会问')
s.cue(b, '预拆以后')

s = Slide('2.1 二倍均值：10 元（1000 分）拆 3 份', src=10)
sub = s.T(40, 72, 920, '`SplitRedPacket`，`repository/cache/redpacket.go:127-167`。'
          '每次随机的上限是**当前剩余人均金额的 2 倍**。', fs=15)
s0 = s.B(40, 118, 240, 34, 'remain = 1000，count = 3', kind='pill')
hit = {}
for y, chip, cells in [
    (172, '第 1 份\n(i = 0)', ['left = 3\n人均 1000 / 3 = 333', 'avg = 333 × 2 = 666\nmaxThisRound = 1000 - 2 = 998',
                              '区间 [1, 665]\n期望 333', '抽中 300']),
    (272, '第 2 份\n(i = 1)', ['left = 2\n人均 700 / 2 = 350', 'avg = 350 × 2 = 700\nmaxThisRound = 700 - 1 = 699',
                              '区间 [1, 699]\n期望 350', '抽中 500'])]:
    s.B(40, y, 130, 56, chip, fill=F2, bold=True)
    for (x, w), t in zip([(190, 200), (410, 270), (700, 150), (870, 90)], cells):
        last = t.startswith('抽中')
        hit[(y, x)] = s.B(x, y, w, 56, t, kind='pill' if last else 'round',
                          fill=NOTE if last else F1, stroke=NOTE_B if last else O1)
        s.A(x - 20, y + 28, x, y + 28)
s.B(40, 372, 130, 56, '第 3 份\n（最后一份）', fill=F2, bold=True)
c1 = s.B(190, 372, 300, 56, '不随机，拿走全部剩余 200', kind='pill', fill=NOTE, stroke=NOTE_B)
s.A(170, 400, 190, 400)
s.B(540, 372, 420, 56, 'Shuffle：[300, 500, 200] 随机打乱')
s.A(490, 400, 540, 400)
s.A(105, 152, 105, 172)
s.A(105, 228, 105, 272, 'remain = 700', lp='right')
s.A(105, 328, 105, 372, 'remain = 200', lp='right')
s.cue(s0)
s.cue(sub, 'gomall 用的是二倍均值法')
s.cue(hit[(172, 700)], '第一份：剩一千分')
s.cue(hit[(272, 700)], '第二份：剩七百分')
s.cue(c1, '第三份不随机')

s = Slide('2.1 二倍均值：保底与洗牌', src=10)
cd = s.code(40, 84, 920, 'go', '''
avg := remain / left * 2                  // 二倍均值上界
maxThisRound := remain - int64(count-1-i) // 保底：后面每份至少留 1 分
hi := avg - 1                             // 再和 maxThisRound 取小
amt := splitRand.r.Int63n(hi) + 1         // 在 [1, hi] 内均匀随机
''')
bl = s.bullets(['纯随机 `[1, 剩余总额]`：第一个人可能拿走 100 元里的 99 元。',
                '最后一份直接拿走全部剩余，总和分毫不差。',
                '`Shuffle`：前面抽得小时最后一份会特别大，打乱后大额不会固定便宜最后一个人。'], 40, 250, 920)
s.cue(cd, '代码里还有一个保底')
s.cue(bl[2], '最后为什么要洗牌')
s.cue(bl[2], '如果前两份运气不好', laser=True)

s = Slide('2.2 抢包 Lua：去重、出队、打标记，一次原子完成', src=11)
cd = s.code(40, 80, 630, 'lua', '''
-- KEYS[1]: redpacket:{id}:amounts (LIST)
-- KEYS[2]: redpacket:{id}:claimed (HASH)
-- ARGV[1]: userID, ARGV[2]: TTL秒数, ARGV[3]: 当前unix秒
if redis.call('HEXISTS', KEYS[2], ARGV[1]) == 1 then
    return -2    -- 1. 已经领过了，直接挡回！
end
local amt = redis.call('LPOP', KEYS[1])
if not amt then
    return -1    -- 2. LIST 空了，红包已抢完！
end
-- 3. 写入抢包标记，如 101 -> "3000:1730680000"
redis.call('HSET', KEYS[2], ARGV[1], amt .. ':' .. ARGV[3])
redis.call('EXPIRE', KEYS[2], tonumber(ARGV[2]))
return tonumber(amt)    -- 4. 返回抢到的金额（分）
''', name='repository/cache/redpacket.go:55-66')
steps = [s.B(690, y, 270, 66, t) for y, t in [
    (84, '① `HEXISTS`：领过了\n返回 -2'), (164, '② `LPOP`：弹不出来，已抢完\n返回 -1'),
    (244, '③ `HSET` 记“金额:时间”\n再 `EXPIRE`'), (324, '④ 返回抢到的金额（分）')]]
atom = s.B(690, 404, 270, 60, '整段 Lua 原子执行\n中间不会被别的命令插进来', fill=NOTE, stroke=NOTE_B)
s.cue(cd)
s.cue(steps[0], '第一步查 HASH')
s.cue(steps[1], '第二步 LPOP')
s.cue(steps[2], '第三步弹出来了')
s.cue(atom, '重点是这三步')

s = Slide('2.2 抢包 Lua：三次调用的结果', src=11)
_, st = s.seq(['Service', 'Redis'], [
    ('m', 0, 1, 'claim Lua (uid=101)'),
    ('m', 1, 0, '(3000) 抢到，标记 101 → 3000:时间', 'r'),
    ('m', 0, 1, 'claim Lua (uid=101) 再点一次'),
    ('m', 1, 0, '(-2) 已领过 → 84005', 'r'),
    ('n', 1, 1, '之后 102、103 把剩下两份抢走，LIST 空了'),
    ('m', 0, 1, 'claim Lua (uid=104)'),
    ('m', 1, 0, '(-1) 已抢完 → 84004', 'r'),
], 90, 500)
s.cue(st[1], '下面这张图是三次调用的结果')
s.cue(st[3], '他再点一次')
s.cue(st[6], '等另外两份被抢走以后')

quiz('随堂测验一：分发模型', [
    ('single', '抢包 Lua 返回 -2，表示什么？',
     ['LIST 空了，红包已抢完', '这个用户已经领过了', '口令不对', 'DB 事务失败'], 'B',
     '脚本第一步用 HEXISTS 查 claimed，领过就返回 -2（对应 84005）。LPOP 弹不出来才是 -1（对应 84004）。'),
    ('single', '二倍均值法里，每次随机的上限是什么？',
     ['剩余总额', '红包总金额的一半', '当前剩余人均金额的 2 倍', '上一份金额的 2 倍'], 'C',
     '每次随机的上限是当前剩余人均金额的 2 倍，期望正好是人均；同时每一轮给后面没拆的每份至少留 1 分，最后一份直接拿走全部剩余。'),
    ('multiple', '为什么金额在发包时一次拆好，抢包时只做一次 LPOP？（多选）',
     ['抢包热路径上没有随机数计算，没有分布式锁', '失败回补就是 LPUSH 原样放回一张，总额不会错',
      '预拆之后抢包就不再需要 DB 事务', '如果抢的时候实时算，回补退回去的金额会让平均值变形'], 'ABD',
     '发包是低频操作，拆金额、扣钱、推进 Redis 都在发包时做；抢包只从 LIST 头上弹一个数。预拆后 LIST 里是固定面额的“代金券”，'
     '抢是拿走一张，回补是原样放回一张。抢到之后仍然要靠 DB 事务落库入账。'),
])

# =====================================================================
# 三、资金模型
# =====================================================================
s = Slide('三、资金模型：钱先进红包托管，再分给领取人', src=12)
sub = s.T(40, 72, 920, '算例：A 发 100 元（`10000` 分）3 份红包（红包 `ID = 88`）。B 抢到 30 元（`claim.ID = 501`），'
          'C 抢到 50 元（`claim.ID = 502`），第 3 份 20 元没人领，24 小时后过期退回 A。', fs=14)
_, st = s.seq(['A 钱包', 'redpacket_escrow', 'B 钱包', 'C 钱包'], [
    ('m', 0, 1, '① 发包 10000 (settleSendTx, ref=88)'),
    ('n', 1, 1, '托管余额 10000'),
    ('m', 1, 2, '② B 领包 3000 (settleClaimTx, ref=501)'),
    ('n', 1, 1, '托管余额 7000'),
    ('m', 1, 3, '③ C 领包 5000 (settleClaimTx, ref=502)'),
    ('n', 1, 1, '托管余额 2000'),
    ('m', 1, 0, '④ 过期退款 2000 (settleRefundTx, ref=88)'),
    ('n', 1, 1, '托管余额 0'),
], 136, 500)
s.T(40, 506, 920, '• 每个箭头是一组**一借一贷**的复式分录，4 个箭头共 8 条流水；发包、领包、退款三段各自独立幂等。',
    fs=14, lines=1)
s.cue(sub)
s.cue(st[0], '钱先从发包人钱包扣出来')
s.cue(st[2], '有人抢到一份')
s.cue(st[6], '过期没领完的')
s.cue(st[1], '看这个例子')
s.cue(st[7], '托管账户最后必须是零', laser=True)

s = Slide('3.1 完整台账：account_transaction 的 8 条流水', src=13)
A_, E_ = '#FFF4E0', F1
hdr, rows, cells = s.grid(40, 78, [110, 150, 130, 180, 70, 90, 190], [
    ['1. 发包', 'settleSendTx', '88 (红包ID)', 'A 的个人钱包', 'debit', '10000', 'redpacket_send'],
    ['1. 发包', 'settleSendTx', '88 (红包ID)', 'redpacket_escrow', 'credit', '10000', 'redpacket_send'],
    ['2. B 领包', 'settleClaimTx', '501 (领取ID)', 'redpacket_escrow', 'debit', '3000', 'redpacket_claim'],
    ['2. B 领包', 'settleClaimTx', '501 (领取ID)', 'B 的个人钱包', 'credit', '3000', 'redpacket_claim'],
    ['3. C 领包', 'settleClaimTx', '502 (领取ID)', 'redpacket_escrow', 'debit', '5000', 'redpacket_claim'],
    ['3. C 领包', 'settleClaimTx', '502 (领取ID)', 'C 的个人钱包', 'credit', '5000', 'redpacket_claim'],
    ['4. 过期退款', 'settleRefundTx', '88 (红包ID)', 'redpacket_escrow', 'debit', '2000', 'redpacket_refund'],
    ['4. 过期退款', 'settleRefundTx', '88 (红包ID)', 'A 的个人钱包', 'credit', '2000', 'redpacket_refund'],
], 36, header=['阶段', '结算函数', 'ref_order_id', '账户 (account_code)', '方向', '金额(分)', 'biz_type'],
    fills=[E_, E_, A_, A_, E_, E_, A_, A_])
net = s.B(40, 432, 920, 44, '`redpacket_escrow` 净余额：+10000 (发包) - 3000 (B领) - 5000 (C领) - 2000 (退回A) = 0',
          fill=NOTE, stroke=NOTE_B, fs=16)
s.cue(hdr)
s.cue(cells[0][2], '大家重点看第三列')
s.cue(cells[2][2], '领包这四行填的是')
s.cue(cells[6][2], '为什么不一样', laser=True)
s.cue(net, '最下面算一下托管账户')

s = Slide('3.2 领包的 ref_order_id 为什么用“领取记录 ID”？', src=14)
sub = s.T(40, 72, 920, '流水表防重唯一索引：`uniq_acct_tx_order_dir (ref_order_id, direction, biz_type)`，'
          '**唯一键里没有 `user_id`**。', fs=15)
s.B(40, 128, 190, 60, '领包也用红包 ID\n做 ref', fill=RED_BG, stroke=RED, color=RED, bold=True)
s.B(245, 128, 280, 60, 'B 领包写入\n(88, credit, redpacket_claim)')
s.B(550, 128, 280, 60, 'C 领包写入\n(88, credit, redpacket_claim)')
b3 = s.B(855, 128, 105, 60, '撞唯一索引\n报错', fill=RED_BG, stroke=RED, color=RED)
s.B(40, 214, 190, 60, '领包用 RedPacketClaim.ID\n做 ref', fill=F2, bold=True)
s.B(245, 214, 280, 60, 'B 领包写入\n(501, credit, redpacket_claim)')
s.B(550, 214, 280, 60, 'C 领包写入\n(502, credit, redpacket_claim)')
g3 = s.B(855, 214, 105, 60, '互不冲突', fill='#FFE066', stroke='#E67700')
for y in (158, 244):
    s.A(525, y, 550, y)
    s.A(830, y, 855, y)
bl = s.bullets(['**发包和过期退款**：一个红包只发一次、最多退一次，用红包 ID (`88`)，'
                '靠 `redpacket_send` / `redpacket_refund` 区分。',
                '**多人领包**：每人用自己那条 `RedPacketClaim.ID`（`501`、`502`）。'], 40, 300, 920)
s.cue(sub)
s.cue(bl[0], '发包和退款，一个红包只会发一次')
s.cue(b3, '但领包是多个人')
s.cue(g3, '所以领包用的是')

s = Slide('3.3 记账顺序：先改用户钱包，后记系统托管', src=15)
cd = s.code(40, 80, 920, 'go', '''
func settleSendTx(tx *gorm.DB, rpID, senderID uint, total int64) (wrote bool, err error) {
    if total <= 0 {
        return false, errors.New("redpacket settle: invalid total")
    }
    // 第 1 腿：先操作用户个人钱包（带行锁 FOR UPDATE + 余额不足检查 + 幂等预检）
    skipped, _, err := adjustWallet(tx, senderID, rpID, -total, money.BizTypeRedPacketSend)
    if err != nil || skipped {
        return false, err
    }
    // 第 2 腿：用户钱包扣成功后，再追加系统托管账户流水
    return true, adjustClearing(tx, rpID, total, money.BizTypeRedPacketSend)
}
''', name='internal/redpacket/settle.go:101-110')
bl = s.bullets(['**先难后易**：钱包一侧要加行锁、解密余额、校验够不够扣，最容易失败；余额不够直接返回，托管表不用碰。',
                '**幂等短路**：重复调用时 `adjustWallet` 查到台账已存在，返回 `skipped = true`，两条腿一起跳过。'],
               40, 412, 920, fs=15)
s.cue(cd)
s.cue(bl[0], '原因有两个')
s.cue(bl[1], '第二，幂等')

# =====================================================================
# 四、一致性边界
# =====================================================================
s = Slide('四、一致性边界：4.1 领取主链路与异常分支', src=16)
_, st = s.seq(['Client', 'RedPacketSrv', 'Redis', 'MySQL'], [
    ('m', 0, 1, 'POST /redpacket/claim (id, code)'),
    ('m', 1, 3, 'Get(id) 校验口令、status、expire_at'),
    ('m', 1, 2, 'claim Lua (uid, now) → amount / -1 / -2', 'b'),
    ('m', 1, 0, '已抢完或已领过：失败 (84004 / 84005)', 'r'),
    ('m', 1, 3, '抢到 amount：claimTx (INSERT claim, remaining-1, 入账, outbox)'),
    ('m', 1, 0, '提交成功：成功 (amount)', 'r'),
    ('n', 1, 3, '提交失败或结果未知', True),
    ('m', 1, 3, 'GetClaim(id, uid)'),
    ('m', 1, 0, '库里已有这条领取：成功 (库里的 amount)', 'r'),
    ('m', 1, 0, '查询出错：失败 (标记留给对账)', 'r'),
    ('m', 1, 0, '红包已结束：失败 (不回补)', 'r'),
    ('m', 1, 2, '确认没落库：rollback Lua (比较标记后放回)，再返回失败 (err)'),
], 76, 552)
s.cue(st[2])
s.cue(st[5], '这张图是一次完整的抢包')
s.cue(st[6], '要看的是下半段')
s.cue(st[7], '注意它没有直接回补')
s.cue(st[11], '为什么要这么麻烦', laser=True)

s = Slide('4.2 无脑回补：一份 30 元被发了两次', src=17)
bad = s.T(40, 74, 920, '错误写法：if err := dbClaimTx(amount); err != nil { redisRollback(amount) }  // 只要报错就无脑塞回 Redis',
          fs=14, color=RED, fill=RED_BG, mono=True, lines=1)
_, st = s.seq(['UserA', 'Service', 'Redis', 'DB', 'UserB'], [
    ('m', 0, 1, '抢红包 (uid=A)'),
    ('m', 1, 2, 'claim Lua (uid=A) → (amount=3000)', 'b'),
    ('m', 1, 3, 'dbClaimTx (uid=A, 3000)'),
    ('n', 3, 3, 'COMMIT 成功，A 钱包 +3000'),
    ('m', 3, 1, '确认包丢失 (driver: bad connection)', 'x'),
    ('m', 1, 2, 'redisRollback (LPUSH 3000)', 'x'),
    ('n', 2, 2, '已发出去的 3000 又回到 LIST', True),
    ('m', 4, 1, '抢红包 (uid=B)'),
    ('m', 1, 2, 'claim Lua (uid=B) → (amount=3000)', 'b'),
    ('m', 1, 3, 'dbClaimTx (uid=B, 3000) → (COMMIT OK，B 钱包 +3000)', 'b'),
    ('n', 2, 3, '同一份 3000 入账两次，托管池被击穿亏空', True),
], 120, 554)
s.cue(bad)
s.cue(st[4], '问题在于')
s.cue(st[3], '看图，A 抢到三十块')
s.cue(st[6], '这时候无脑回补')
s.cue(st[9], '下一个人 B 来了')
s.cue(st[10], '同一份三十块发了两次', laser=True)

s = Slide('4.2 正确做法：四分支判定', src=18)
s.T(310, 76, 400, '`internal/redpacket/service.go:213-238`', fs=14, lines=1)
s.B(720, 80, 240, 48, '四个分支里**只有分支 4 会回补**', fill=NOTE, stroke=NOTE_B)
s.B(40, 82, 250, 36, 'doClaimTx 返回 txErr', kind='pill')
q = s.B(40, 134, 250, 36, '反查 DB：GetClaim(rp.ID, uid)')
s.B(40, 188, 250, 44, '反查本身报错？', kind='hex', fill=F2)
s.B(40, 264, 250, 44, '库里已有记录？', kind='hex', fill=F2)
s.B(40, 340, 250, 56, 'txErr 是\nerrRedPacketClosed？', kind='hex', fill=F2)
b4 = s.B(40, 426, 250, 58, '分支 4：确认未落库且红包进行中\n执行回补 Lua', fill=NOTE, stroke=NOTE_B)
s.A(165, 118, 165, 134)
s.A(165, 170, 165, 188)
for y1, y2 in [(232, 264), (308, 340), (396, 426)]:
    s.A(165, y1, 165, y2, '否', lp='right')
b1 = s.B(350, 181, 330, 58, '分支 1：不回补\n保留 claimed 标记，交给对账兜底')
b2 = s.B(350, 257, 330, 58, '分支 2：假失败\n按成功返回库里的金额，不回补', kind='pill')
b3 = s.B(350, 339, 330, 58, '分支 3：红包已结束\n剩余已按 DB 退给发包人，不回补')
for y in (210, 286, 368):
    s.A(290, y, 350, y, '是')
s.B(780, 270, 180, 40, '返回 txErr', kind='pill', fill=RED_BG, stroke=RED, color=RED)
s.L(680, 210, 870, 270, via=(870, 210))
s.L(680, 368, 870, 310, via=(870, 368))
s.B(350, 426, 250, 58, '回补返回\nErrRedPacketClaimRepairing？', kind='hex', fill=F2)
rq = s.B(650, 426, 170, 58, '再查 GetClaim\n对账已帮他落库？', kind='hex', fill=F2)
s.B(866, 426, 94, 58, '按成功\n返回', kind='pill')
s.A(290, 455, 350, 455)
s.A(600, 455, 650, 455, '是')
s.A(820, 455, 866, 455, '是')
s.B(350, 508, 250, 34, '否 → 返回 txErr', kind='pill', fill=RED_BG, stroke=RED, color=RED)
s.A(475, 484, 475, 508)
s.L(735, 484, 600, 525, via=(735, 525))
s.cue(q)
s.cue(b1, '第一，连反查都报错')
s.cue(b2, '第二，库里查到了记录')
s.cue(b3, '第三，事务报的是红包已结束')
s.cue(b4, '第四，确认库里没有')
s.cue(rq, '分支四后面还有个小尾巴')

LAB_AT = len(SCENES)   # 互动实验放在四分支之后，HTML 在文件末尾定义

s = Slide('4.3 工单 B：抢最后一份与过期退款同时发生（情况一）', src=19, title_fs=26)
sub = s.T(40, 72, 920, '场景：100 元红包 `12:00:00` 到期，前两人领走 80 元，还剩最后一份 20 元。同一毫秒，用户 C 在执行 `claimTx`，'
          '过期回收任务在执行 `refundRedPacketLeft`（`internal/redpacket/task.go:59-105`）。情况一，领取事务先拿到行锁：', fs=14)
ps, st = s.seq(['领取事务\n(用户 C 抢最后 20 元)', 'red_packet\n主表行锁', '过期退款事务\n(Cron)'], [
    ('m', 0, 1, '1. INSERT claim (amount=2000)'),
    ('m', 0, 1, '2. UPDATE remaining-1 WHERE status=active (拿到行锁!)'),
    ('m', 2, 1, 'SELECT ... FOR UPDATE (请求同一行锁)'),
    ('n', 1, 2, '被阻塞！排队等待领取事务释放锁...'),
    ('m', 0, 1, '3. 标记 status=finished + 转账 + COMMIT!'),
    ('m', 1, 2, '终于拿到行锁！读到最新 status = finished', 'r'),
    ('s', 2, '发现已是 finished，直接退出，退还 0 元！'),
], 138, 530)
s.cue(sub)
s.cue(ps[2], '就在十二点整这一毫秒')
s.cue(ps[1], '两边怎么不撞车')
s.cue(st[1], '先看领取事务先到的情况')
s.cue(st[3], '退款事务的第一句')
s.cue(st[6], '等领取事务提交了')

s = Slide('4.3 情况二：退款事务先拿到行锁', src=20)
_, st = s.seq(['Refund', 'DB', 'Claim', 'Service'], [
    ('m', 0, 1, 'SELECT ... FOR UPDATE (id=88)'),
    ('m', 1, 0, '(status=active，拿到行锁)', 'r'),
    ('m', 2, 1, 'INSERT claim (amount=2000)'),
    ('m', 2, 1, 'UPDATE remaining-1 WHERE status=active'),
    ('n', 1, 2, '被阻塞，等退款事务释放行锁'),
    ('m', 0, 1, 'SUM(claim.amount)，算出 left = Total - SUM'),
    ('m', 0, 1, 'status=refunded + 退 left 给发包人 + COMMIT'),
    ('m', 1, 2, '(RowsAffected = 0)', 'r'),
    ('m', 2, 3, 'errRedPacketClosed，整个领取事务回滚', 'r'),
    ('n', 3, 3, '走四分支的分支 3，不回补 Redis'),
], 76, 500)
sql = s.T(40, 504, 920, 'UPDATE red_packet SET remaining = remaining - 1 WHERE id = ? AND remaining > 0 AND status = 1 /* active */',
          fs=14, mono=True, fill=F1, lines=1)
s.cue(st[6])
s.cue(sql, '这时候领取事务的 UPDATE')
s.cue(st[8], '现在状态已经是已退款')
s.cue(st[9], '回到上层')
s.cue(st[4], '所以不管谁先谁后', laser=True)

s = Slide('4.4 为什么 FOR UPDATE 必须是事务第一条语句？', src=21)
sub = s.T(40, 70, 920, 'InnoDB 默认 **RR（可重复读）**：事务里**第一条普通 `SELECT`** 建立 ReadView 快照，此后所有普通读（含 `SUM`）只看得到快照；'
          '`SELECT ... FOR UPDATE` **不建快照**，等行锁，读最新已提交数据。反面推演：红包 `88` 共 100 元 3 份，B 已领 30 元；'
          '到期瞬间 C 抢到 50 元。', fs=14)
_, st = s.seq(['过期退款事务\n(首行错加了普通 SELECT)', 'MySQL\n(InnoDB RR 隔离级别)', '用户 C 领取事务\n(抢到 50 元)'], [
    ('m', 0, 1, '1. SELECT * FROM red_packet WHERE id=88 (普通读)'),
    ('n', 0, 1, '致命点：RR 快照在此刻建立！快照里已领金额 SUM = 30 元', True),
    ('m', 2, 1, '2. INSERT claim(50元) + UPDATE remaining-1 + COMMIT'),
    ('n', 1, 2, '用户 C 成功领走 50 元！真实已领变为 80 元'),
    ('m', 0, 1, '3. SELECT * FROM red_packet WHERE id=88 FOR UPDATE'),
    ('n', 0, 1, '加锁读看到最新行：status 仍是 active，继续往下走'),
    ('m', 0, 1, '4. SELECT SUM(amount) FROM red_packet_claim WHERE red_packet_id=88'),
    ('n', 0, 1, '灾难发生：SUM 是普通读，复用第 1 步的旧快照！算出来 SUM 仍是 30 元！', True),
    ('m', 0, 1, '5. 算出应退 left = 100 - 30 = 70 元，退给发包人并 COMMIT！'),
    ('n', 0, 2, '资损结果：100 元红包被领走 80 元，又退了 70 元！', True),
], 138, 556)
s.cue(s.tid)
s.cue(sub, '为什么？这是 MySQL')
s.cue(st[1], '现在假设有人')
s.cue(st[2], '第二步，C 抢到五十')
s.cue(st[7], '第三步加锁读')
s.cue(st[9], '第五步，一百减三十')

s = Slide('4.4 正确顺序：快照在拿到锁之后才建', src=22)
_, st = s.seq(['Refund', 'DB', 'Claim'], [
    ('m', 2, 1, 'INSERT claim (5000) + UPDATE remaining-1'),
    ('m', 0, 1, '1. SELECT ... FOR UPDATE (事务第一条语句)'),
    ('n', 0, 1, '加锁读不建快照，排队等行锁'),
    ('m', 2, 1, 'COMMIT'),
    ('m', 1, 0, '(拿到行锁，status=active)', 'r'),
    ('m', 0, 1, '2. SELECT SUM(amount) (第一条普通读)'),
    ('n', 0, 1, '快照此刻才建立，看得到 C 刚提交的 50 元'),
    ('m', 1, 0, '(SUM = 8000)', 'r'),
    ('m', 0, 1, '3. left = 10000 - 8000 = 2000，退给发包人 + COMMIT'),
], 76, 468)
bl = s.bullets(['拿到行锁 = 排在它前面的领取事务都已 `COMMIT`。',
                '之后的第一条普通读 `SumClaimed` 建的快照，一定包含这些领取。'], 40, 474, 920, fs=14, gap=6)
s.cue(st[1])
s.cue(bl[0], '等它拿到锁')
s.cue(st[6], '这时候再执行求和')
s.cue(st[8], '同样两条 SQL')

s = Slide('4.5 工单 A：回补 Lua 与 :r 补领锁', src=23)
cd = s.code(40, 80, 630, 'lua', '''
-- KEYS[1]: redpacket:{id}:amounts (LIST)
-- KEYS[2]: redpacket:{id}:claimed (HASH)
-- ARGV[1]: userID, ARGV[2]: 刚才抢到的金额 amount
local v = redis.call('HGET', KEYS[2], ARGV[1])
if not v then
    return 0     -- 标记已经不在了（已被回补过），幂等返回
end
if string.sub(v, -2) == ':r' then
    return -1    -- 末尾有 ":r"，对账已接手补领，绝不放回
end
redis.call('LPUSH', KEYS[1], ARGV[2]) -- 金额原样塞回 LIST 头部
redis.call('HDEL', KEYS[2], ARGV[1])  -- 删已领标记，允许重抢
return 1
''', name='repository/cache/redpacket.go:92-99')
r0 = s.B(690, 84, 270, None, '**返回 0**\n标记不存在，已回补过\n→ 幂等返回，什么都不做')
r1 = s.B(690, 172, 270, None, '**返回 -1**\n标记末尾有 `:r`，对账已接手\n→ 不放回', fill=RED_BG, stroke=RED)
r2 = s.B(690, 260, 270, None, '**返回 1**\n普通标记\n→ `LPUSH` 放回金额 + `HDEL` 删标记，用户可重抢',
         fill=NOTE, stroke=NOTE_B)
sub = s.T(40, 462, 920, '确认 DB 真的失败且未落库时，调 `RollbackRedPacketClaim`。', fs=14, lines=1)
s.cue(sub)
s.cue(cd, '解决它的就是这段回补脚本')
s.cue(r0, '标记不在了')
s.cue(r1, '标记末尾有冒号 r')
s.cue(r2, '只有普通标记才真正回补')
s.cue(r2, '删掉标记这一步', laser=True)

quiz('随堂测验二：一致性边界', [
    ('single', 'DB 事务报错后先反查 GetClaim。四个分支里，哪一个会回补 Redis？',
     ['反查本身也报错', '库里已有这条领取记录', 'txErr 是 errRedPacketClosed（红包已结束）', '确认未落库，且红包还在进行中'], 'D',
     '四个分支里只有分支 4 会回补。反查报错时什么都不动，标记留给对账；库里有记录是假失败，按成功返回；'
     '红包已结束时剩余已按 DB 退给发包人，这一份不能再放回去。'),
    ('single', '过期退款事务里，SELECT ... FOR UPDATE 为什么必须是事务的第一条语句？',
     ['否则会和领取事务死锁',
      'RR 下第一条普通 SELECT 会建立快照，之后的 SUM 复用旧快照，看不到刚提交的领取，会多退钱',
      'FOR UPDATE 写在后面会有语法错误', '为了让退款事务比领取事务先执行'], 'B',
     '加锁读不建快照，只是排队等锁；拿到锁说明前面的领取事务都已 COMMIT，这时第一条普通读 SumClaimed 建的快照一定包含这些领取。'
     '如果前面先插了一句普通查询，快照建早了：讲义里的例子是 100 元红包领走 80 元，又退了 70 元。'),
    ('multiple', '关于回补 Lua 的返回值，哪些说法是对的？（多选）',
     ['返回 0：标记不存在，已回补过，幂等返回', '返回 -1：标记末尾有 :r，对账已接手，不放回',
      '返回 1：LPUSH 放回金额 + HDEL 删标记，用户可以重抢', '返回 -1 时仍要把金额 LPUSH 回 LIST'], 'ABC',
     '脚本先读这个用户的标记再动手。带 :r 的标记说明后台对账已经接手补领，这时再放回去就会多发一份。'),
])

# =====================================================================
# 五、三道兜底防线
# =====================================================================
s = Slide('五、三道兜底防线：5.1 红包状态机', src=24)
s.B(72, 112, 16, 16, kind='dot', fill=INK, stroke=INK)
act = s.B(190, 96, 160, 48, 'active', bold=True)
s.B(700, 96, 160, 48, 'finished', bold=True)
s.B(700, 190, 160, 48, 'refunded', bold=True)
s.A(88, 120, 190, 120, '发包事务提交')
s.A(350, 120, 700, 120, '领取事务中 remaining 归零')
s.L(270, 144, 700, 214, via=(270, 214))
why = s.T(300, 181, 380, '过期回收 / 份额池丢失 / Redis 准备失败补偿', fs=14, align='center', lines=1)
for y in (120, 214):
    s.A(860, y, 912, y)
    s.B(912, y - 8, 16, 16, kind='dot', fill=INK, stroke=INK)
hdr, rows, _ = s.grid(40, 284, [300, 250, 370], [
    ['5.2 孤儿对账补领', '每 2 分钟', '工单 C：抢到了但钱没到账'],
    ['5.3 份额池丢失补偿', '发包当场 + 每 2 分钟', '工单 D：扣了钱但没人能抢'],
    ['5.4 过期回收', '每 5 分钟', '没领完的钱退回发包人'],
], 42, header=['防线', '触发', '解决'], fs=16)
s.cue(hdr)
s.cue(act, '先看状态机')
s.cue(why, '退款有三个来源')
s.cue(rows[0], '下面三道防线')

s = Slide('5.2 工单 C：孤儿领取与对账补领', src=25)
sub = s.T(40, 70, 920, '**孤儿领取**：Redis Lua 抢包成功，紧接着服务器断电重启，或 DB 事务超时且回补也因网络抖动失败。'
          '`LIST` 里已经没有这 30 元，`red_packet_claim` 里没有用户 101 的记录，`claimed` 里一直挂着 101。', fs=14)
start = s.B(40, 138, 300, 36, '遍历 claimed 里每条标记', kind='pill')
s.B(40, 192, 300, 44, 'DB 里已有领取记录？', kind='hex', fill=F2)
s.B(40, 264, 300, 52, '抢到不足 1 分钟？\nclaimOrphanGrace', kind='hex', fill=F2)
lock = s.B(40, 344, 300, 52, 'CAS 抢占：标记追加 :r\nLockRedPacketClaimForRepair', kind='hex', fill=F2)
tx = s.B(40, 424, 300, 52, '替用户调同一个 claimTx\n把这份金额补发到钱包', fill=NOTE, stroke=NOTE_B)
s.A(190, 174, 190, 192)
s.A(190, 236, 190, 264, '没有', lp='right')
s.A(190, 316, 190, 344, '否', lp='right')
s.A(190, 396, 190, 424, '拿到 :r 锁', lp='right')
skip = s.B(430, 195, 250, 38, '正常，跳过', kind='pill')
pend = s.B(430, 271, 250, 38, '在途，pending = true，先别动', kind='pill')
s.B(430, 351, 250, 38, '原请求刚完成回补，跳过', kind='pill')
s.B(430, 431, 250, 38, 'repaired++', kind='pill')
s.A(340, 214, 430, 214, '有')
s.A(340, 290, 430, 290, '是')
s.A(340, 370, 430, 370, '改写失败')
s.A(340, 450, 430, 450)
s.B(710, 192, 250, None, '`RunRedPacketClaimReconcile`\n每 2 分钟一次\n`internal/redpacket/task.go:179-211`',
    fill=NOTE, stroke=NOTE_B)
s.cue(sub)
s.cue(start, '这时候三十块不在 LIST 里')
s.cue(skip, '对账任务每两分钟跑一次')
s.cue(pend, '库里没有，但抢到还不到一分钟')
s.cue(lock, '超过一分钟还没落库')
s.cue(tx, '注意是补给抢到的人')

s = Slide('5.2 :r 锁互斥（一）：对账先到', src=26)
sub = s.T(40, 70, 920, '用户请求卡了整整 1 分钟才走失败回补，恰好对账协程也扫到这个标记：', fs=14, lines=1)
_, st = s.seq(['慢请求协程\n(准备回补放回份额)', 'Redis\nclaimed 标记', '后台对账协程\n(准备替用户补领入账)', 'MySQL'], [
    ('n', 1, 1, '当前标记值: "3000:1730680000"'),
    ('m', 2, 1, '1. LockForRepair Lua (对比原值一致，追加 :r)'),
    ('n', 1, 1, '标记变为: "3000:1730680000:r"'),
    ('m', 1, 2, '返回 1 (加锁成功!)', 'r'),
    ('m', 2, 3, '2. 执行 claimTx 替用户落库并加钱 30 元，COMMIT!'),
    ('m', 0, 1, '3. 慢请求终于醒来，执行 rollback Lua'),
    ('n', 0, 1, '发现标记末尾是 ":r"，拒绝 LPUSH 放回！', True),
    ('m', 1, 0, '返回 -1 (ErrRedPacketClaimRepairing)', 'r'),
    ('m', 0, 3, '4. 慢请求再次反查 GetClaim，发现对账已帮他落库！'),
    ('s', 0, '直接向客户端返回：抢包成功 (30元)！'),
], 110, 554)
s.cue(sub)
s.cue(st[0], '一个要把钱放回 Redis')
s.cue(st[1], '先看对账先到')
s.cue(st[6], '慢请求醒来执行回补')
s.cue(st[8], '慢请求收到这个返回值')
s.cue(st[9], '这就是四分支里', laser=True)

s = Slide('5.2 :r 锁互斥（二）：回补先到', src=27)
_, st = s.seq(['Service', 'Redis', 'Reconcile', 'DB'], [
    ('m', 2, 1, 'HGETALL claimed'),
    ('m', 1, 2, '(101 → 3000:1730680000)', 'r'),
    ('m', 0, 1, 'rollback Lua (uid=101, 3000)'),
    ('n', 1, 1, '普通标记：LPUSH 3000 回 LIST + HDEL 101'),
    ('m', 1, 0, '(1 已回补)', 'r'),
    ('m', 2, 1, 'LockForRepair Lua (uid=101, 原值)'),
    ('n', 1, 1, '标记已不存在，和原值对不上'),
    ('m', 1, 2, '(0 抢占失败)', 'r'),
    ('n', 2, 3, '跳过这一份，不调 claimTx'),
], 76, 498)
b = s.T(40, 504, 920, '• 两段 Lua 都**先看标记再动手**：谁先改到标记谁处理这一份，另一方让路。', fs=14, lines=1)
s.cue(st[3])
s.cue(st[6], '对账接着去加锁')
s.cue(st[8], '对账就跳过这一份')
s.cue(b, '所以规则很简单')

s = Slide('5.3 工单 D：份额池丢失的两道防线', src=28)
sub = s.T(40, 70, 920, '`redpacket:{id}:amounts` 这个 `LIST` 不存在，别人一点开就提示“红包已抢完”。', fs=14, lines=1)
s.B(40, 116, 440, 34, '防线 1：发包当场立即逆向退款 (service.go:137-146)', fill=F2, bold=True)
s.B(60, 166, 400, 34, 'Create：DB 扣款事务已提交', kind='pill')
s.B(60, 222, 400, 44, 'PrepareRedPacket 写 Redis 成功？', kind='hex', fill=F2)
s.B(60, 304, 130, 36, '红包可抢', kind='pill')
a4 = s.B(210, 304, 270, None, '当场调 refundRedPacketLeft(ctx, rp.ID)\nredpacket_escrow 全额退回发包人，状态置 refunded',
         fill=NOTE, stroke=NOTE_B)
s.A(260, 200, 260, 222)
s.A(125, 266, 125, 304, '成功', lp='right')
s.A(345, 266, 345, 304, '失败', lp='right')
s.T(40, 412, 440, '补偿也失败 / 运行中途 Redis 丢数据 → 交给防线 2', fs=14, lines=1)
s.L(400, 433, 500, 152, style='dashed', via=(500, 433))
p2 = s.B(490, 116, 470, 34, '防线 2：对账发现池丢失提前退款 (task.go:155-229)', fill=F2, bold=True)
s.B(560, 166, 360, 32, '每 2 分钟对账', kind='pill')
for i, t in enumerate(['创建已超过 1 分钟？', 'amounts LIST 不存在？', '没有在途孤儿标记？', 'DB 里仍 active 且 remaining > 0？']):
    s.B(560, 220 + i * 54, 360, 32, t, kind='hex', fill=F2)
    s.A(740, 198 + i * 54, 740, 220 + i * 54, '是' if i else None, lp='right')
b6 = s.B(560, 436, 360, 52, '提前退款\n不用让发包人傻等 7 天过期', fill=NOTE, stroke=NOTE_B)
s.A(740, 414, 740, 436, '是', lp='right')
s.cue(sub)
s.cue(a4, '这里有两道防线')
s.cue(p2, '第二道在对账任务里')
s.cue(b6, '那就说明份额池丢了')

s = Slide('5.3 池丢失检查：正确顺序（先 EXISTS，后读 claimed）', src=29, title_fs=26)
sub = s.T(40, 70, 920, '难点：最后一份被**正常抢走**时，`LPOP` 弹出最后一个元素，Redis 自动删除空 `LIST`，`EXISTS amounts` 同样是 `false`。',
          fs=14, lines=1)
_, st = s.seq(['Service', 'Redis', 'Reconcile', 'DB'], [
    ('n', 1, 1, 'amounts 只剩最后 1 份'),
    ('m', 0, 1, 'claim Lua (uid=Z)'),
    ('n', 1, 1, 'LPOP 和 HSET claimed 原子完成，空 LIST 被自动删除'),
    ('m', 1, 0, '(amount=2000)', 'r'),
    ('m', 2, 1, '1. EXISTS amounts → (false)', 'b'),
    ('m', 2, 1, '2. HGETALL claimed → (一定含 Z 的标记，抢到不足 1 分钟)', 'b'),
    ('m', 2, 3, 'ListClaims (id) → (还没有 Z)', 'b'),
    ('n', 2, 2, 'Z 在途，r.pending = true，判定为正常抢完，不退款'),
    ('m', 0, 3, 'claimTx (uid=Z, 2000) → (COMMIT OK)', 'b'),
], 110, 550)
s.cue(sub)
s.cue(st[4], '怎么区分')
s.cue(st[2], '看图，用户 Z')
s.cue(st[4], '对账查 LIST，不存在', laser=True)
s.cue(st[5], '然后读标记')
s.cue(st[7], '对账看到有在途标记')

s = Slide('5.3 池丢失检查：错误顺序（先读 claimed，后 EXISTS）', src=30, title_fs=26)
_, st = s.seq(['Service', 'Redis', 'Reconcile', 'DB'], [
    ('n', 1, 1, 'amounts 只剩最后 1 份，还没人抢'),
    ('m', 2, 1, '1. HGETALL claimed → (没有 Z 的标记)', 'b'),
    ('m', 0, 1, 'claim Lua (uid=Z) → (amount=2000)', 'b'),
    ('n', 1, 1, '微秒间隙里 Z 抢走最后 1 份，LIST 变空消失', True),
    ('m', 2, 1, '2. EXISTS amounts → (false)', 'b'),
    ('n', 2, 2, '没有在途标记 + LIST 不存在，误以为 Redis 数据丢了', True),
    ('m', 2, 3, 'Get (id) → (active，remaining = 1)', 'b'),
    ('m', 2, 3, 'refundRedPacketLeft (退 2000 给发包人，status=refunded)', 'x'),
    ('m', 0, 3, 'claimTx (uid=Z, 2000) → (errRedPacketClosed)', 'b'),
    ('n', 0, 3, 'Z 正在入账的这笔钱被当成未领余额退给了发包人', True),
], 76, 552)
s.cue(st[1])
s.cue(st[3], '就在这之后的一瞬间')
s.cue(st[4], '对账接着查 LIST')
s.cue(st[7], '它手里的信息是')
s.cue(st[8], '等 Z 的领取事务')
s.cue(st[9], 'Z 明明抢到了', laser=True)

s = Slide('5.4 过期回收：先补齐孤儿，再按 DB 退剩余', src=31)
sub = s.T(40, 70, 920, '`RunRedPacketExpireCheck` 每 5 分钟一次（`internal/redpacket/task.go:32-50`）', fs=14, lines=1)
s.B(40, 116, 300, 34, '扫出已过期的红包', kind='pill')
r = s.B(40, 168, 300, 52, '第 1 步：reconcileRedPacket\n先把孤儿标记补发给领取人', fill=NOTE, stroke=NOTE_B)
s.B(40, 240, 300, 40, '对账出错或还有在途标记？', kind='hex', fill=F2)
d2 = s.B(420, 234, 240, 52, '过期不足 1 小时？\nrefundHoldForReconcile', kind='hex', fill=F2)
s.B(740, 240, 220, 40, '本轮跳过，下轮再看', kind='pill')
s.B(40, 334, 300, 40, '对账里已提前退款？r.refunded', kind='hex', fill=F2)
s.B(420, 336, 120, 36, '结束', kind='pill')
f = s.B(40, 402, 300, 68, '第 2 步：refundRedPacketLeft\n严格按 MySQL：Total - SUM(claim.amount)', fill=NOTE, stroke=NOTE_B)
s.B(420, 410, 300, 52, '退回发包人，状态 refunded\n提交后清 Redis 剩余份额')
s.A(190, 150, 190, 168)
s.A(190, 220, 190, 240)
s.A(340, 260, 420, 260, '是')
s.A(660, 260, 740, 260, '是')
s.A(190, 280, 190, 334, '否', lp='right')
s.L(430, 286, 338, 336)
s.T(440, 282, 130, '否，超时保底', fs=14, lines=1)
s.A(340, 354, 420, 354, '是')
s.A(190, 374, 190, 402, '否', lp='right')
s.A(340, 436, 420, 436)
bl = s.bullets(['顺序铁律：**必须先补孤儿，再退剩余**。先退了剩余，状态变成 `refunded`，卡在半空的孤儿用户就再也补不进去。',
                '还有在途未决的标记时最多宽限 1 小时，超时保底退回发包人。'], 40, 474, 920, fs=14, gap=6)
s.cue(sub)
s.cue(r, '第一步，先跑一遍对账')
s.cue(f, '第二步，再按数据库算')
s.cue(bl[0], '为什么必须先补孤儿')
s.cue(d2, '还有一个细节')

quiz('随堂测验三：兜底防线', [
    ('single', '判断份额池丢没丢时，为什么必须先 EXISTS amounts，后读 claimed？',
     ['EXISTS 比 HGETALL 快，先做省时间',
      '抢包 Lua 里 LPOP 和打标记是原子的：LIST 若是被正常抢空，之后再读 claimed 一定能看到在途标记，不会误判成池丢失',
      'claimed 有 TTL，晚一点读才准', '两次读取的先后顺序不影响结论'], 'B',
     'LIST 不存在有两种可能：真丢了，或者最后一份刚被正常抢走（Redis 会自动删掉空 LIST）。顺序反过来时，'
     '对账可能在读完标记之后、查 LIST 之前被人抢走最后一份，于是误判池丢失，把正在入账的钱退给发包人。'),
    ('single', '领包流水的 ref_order_id 用的是什么？',
     ['红包 ID', '领取人的 user_id', '领取记录 RedPacketClaim.ID', '发包人的 user_id'], 'C',
     '流水表唯一索引是 (ref_order_id, direction, biz_type)，里面没有 user_id。多人领同一个红包，如果都用红包 ID，'
     '第二个人就会撞唯一索引；所以领包用每人自己那条领取记录的 ID（讲义例子里的 501、502）。发包和退款才用红包 ID。'),
    ('single', '过期回收对每个过期红包做两步，顺序是什么？',
     ['先按 DB 退剩余，再跑对账补孤儿', '先跑对账把孤儿补给领取人，再按 DB 算 Total - SUM(claim.amount) 退给发包人',
      '两步并行执行', '只退剩余，孤儿留给下一轮'], 'B',
     '一旦退款，状态变成 refunded，抢到了却卡在半空的用户的领取事务会被拒，这份钱就再也补不给他。'
     '对账出错或还有在途标记时本轮先不退，最多宽限 1 小时，超时保底退回发包人。'),
])

# =====================================================================
# 六、故障演练、课后思考、代码索引
# =====================================================================
for i, (title, cmd, brk, seen, cues) in enumerate([
    ('六、故障演练 · 演示 1：过期退款严格按 DB 计算',
     'go test ./internal/redpacket -run TestExpireRefundsLeftoverFromDBWhenRedisIsEmpty -v',
     '打开 `internal/redpacket/task.go`，把 `left = rp.Total - claimed` 改成使用 Redis 清理脚本返回的剩余金额。',
     '测试立刻失败。测试故意把 Redis 清空了，按 Redis 算就会退 0 元，发包人的钱就丢了。',
     [('how', ''), ('brk', '第一个，把退款金额'), ('seen', '测试里 Redis 是空的')]),
    ('六、故障演练 · 演示 2：回补与对账补领的 :r 锁互斥',
     "go test ./repository/cache -v -run \\\n"
     "  'TestRollbackAndRepairLockAreExclusive|TestRepairLockRequiresOriginalMark'\n"
     "go test ./internal/redpacket -v -run \\\n"
     "  'TestClaimTxFailureRollsBackRedis|TestClaimCommitUnknownDoesNotRollback'\n"
     "go test ./internal/redpacket -v -run TestClaimRollbackYieldsToReconcileRepair",
     "把 `repository/cache/redpacket.go:95` 回补 Lua 里的 `if string.sub(v, -2) == ':r' then return -1 end` 注释掉，"
     '再跑 `TestClaimRollbackYieldsToReconcileRepair`。',
     '对账帮用户补领入账后，慢请求又把份额塞回 Redis，触发 `"redis has an extra share"` 超发断言失败。',
     [('brk', '第二个，把回补脚本里'), ('seen', '对账补领以后慢请求'), ('seen', '测试报 Redis 多了一份')]),
    ('六、故障演练 · 演示 3：真 MySQL 下的行锁与 MVCC 快照时机',
     "REDPACKET_TEST_MYSQL_DSN='root@tcp(127.0.0.1:3306)/rptest?charset=utf8mb4&parseTime=True&loc=Local' \\\n"
     '  go test ./internal/redpacket -run TestClaimAndRefundRaceConservesMoney -count=3 -v',
     '在 `internal/redpacket/task.go:67` 的 `txDao.GetForUpdate(rpID)` 前面插入一行普通查询 `_, _ = txDao.Get(rpID)`，'
     '在真 MySQL 下重跑。',
     '立刻复现 `claimed X + refunded Y != 2000` 的双倍退钱资损事故。',
     [('brk', '第三个，在加锁读前面'), ('seen', '测试报领取加退款'), ('cmd', '注意第三个必须连真的 MySQL')]),
]):
    s = Slide(title, src=32, title_fs=26)
    el = {'how': s.B(40, 80, 920, 40, '每个演示都是：**先跑通测试，再动手破坏一行代码，看测试变红。**',
                     fill=NOTE, stroke=NOTE_B, fs=16)}
    el['cmd'] = s.code(40, 134, 920, 'bash', cmd, numbers=False)
    y = 134 + s.els[-1]['height'] + 16
    el['brk'] = s.B(40, y, 920, 78, '**破坏**：' + brk, fs=16, align='left')
    el['seen'] = s.B(40, y + 94, 920, 78, '**现象**：' + seen, fs=16, align='left', fill=RED_BG, stroke=RED)
    for j, (k, start) in enumerate(cues):
        s.cue(el[k], start, laser=(j == 2 and cues[1][0] == k))

s = Slide('课后思考与练习', src=33)
qs, y = [], 80
for t in [
    '**1. 在途窗口调参推演**：`claimOrphanGrace` 设为 1 分钟。如果某次数据库发生慢查询锁等待，导致一个正常领取事务卡了整整 90 秒才报错进入回补分支，'
    '请结合 4.2 和 5.2 节推演：这 90 秒里对账任务做了什么？慢请求醒来去回补时会收到什么返回值？用户最终看到成功还是失败？',
    '**2. 设计口令轮换功能**：发包人把红包链接发错群了，想立刻换一个新口令（`POST /redpacket/rotate-code`）。请设计该接口：'
    '之前已经用旧口令领到红包的人还能不能打开详情页看明细？并发正在抢的人会受什么影响？',
    '**3. 验证二倍均值分布**：写一段小测试调用 `SplitRedPacket(10000, 10)` 十万次，统计第 1 个位置到第 10 个位置的平均金额和最大金额。'
    '如果把代码里的 `Shuffle` 删掉，第 10 个位置的最大值分布会发生什么变化？',
    '**4. 编写托管池守恒巡检 SQL**：写一条只读巡检 SQL，找出所有状态已经是 `finished` 或 `refunded`、但在 `account_transaction` 表中 '
    '`redpacket_escrow` 借贷净额不为 0 的异常红包（提示：发包/退款流水的 `ref_order_id` 是红包 ID，领包流水的 `ref_order_id` 是 `red_packet_claim.id`）。',
]:
    qs.append(s.B(40, y, 920, None, t, align='left'))
    y += s.els[-1]['height'] + 10
end = s.B(40, y + 4, 920, 44, 'Redis 决定谁拿到哪一份，MySQL 决定钱有没有动，两边对不上的时候，永远听 MySQL 的。',
          fill=NOTE, stroke=NOTE_B, fs=16, bold=True)
s.cue(qs[0])
s.cue(qs[1], '第二道是设计题')
s.cue(qs[2], '第三道动手')
s.cue(qs[3], '第四道写一条巡检 SQL')
s.cue(end, '最后用一句话收尾')

s = Slide('附录：核心代码索引')   # 讲义这一页没有讲稿，所以没有 speech
s.grid(40, 76, [330, 590], [
    ['Redis 存储结构与 4 段 Lua 脚本', 'repository/cache/redpacket.go'],
    ['二倍均值预拆算法 SplitRedPacket', 'repository/cache/redpacket.go'],
    ['孤儿标记解析与份额池检查', 'repository/cache/redpacket.go'],
    ['红包与领取记录表模型', 'internal/redpacket/model.go'],
    ['发包 / 抢包 / 详情 / 列表 / 手动对账', 'internal/redpacket/service.go'],
    ['防扫号口令生成与恒定时间比对', 'internal/redpacket/service.go'],
    ['DB 条件扣减与行锁查询', 'internal/redpacket/repo.go'],
    ['三段式托管资金流复式记账', 'internal/redpacket/settle.go'],
    ['过期回收与孤儿对账定时任务', 'internal/redpacket/task.go'],
    ['台账对账消费者', 'internal/redpacket/consumer.go'],
    ['路由与限流/幂等护栏', 'internal/redpacket/routes.go'],
    ['系统托管账户定义', 'internal/money/ledger_model.go'],
    ['错误码定义 84001 ~ 84006', 'pkg/e/code.go'],
    ['单元与并发测试集', 'service_test.go、claim_test.go、repository/cache/redpacket_test.go'],
], 29, header=['业务模块 / 技术点', '文件路径'])

# =====================================================================
# 互动实验：自包含 HTML，无外部依赖、无网络请求
# =====================================================================
LAB_HTML = r'''<!doctype html>
<html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>DB 报错后：回补还是先查库</title>
<style>
*{box-sizing:border-box}
body{margin:0;padding:20px;background:#FFF8EE;color:#3D1F0B;font:15px/1.6 "PingFang SC","Microsoft YaHei",sans-serif}
h1{font-size:22px;margin:0 0 4px;color:#D9480F}
.lead{margin:0 0 14px}
.grid{display:grid;grid-template-columns:repeat(3,1fr);gap:12px}
@media(max-width:720px){.grid{grid-template-columns:1fr}}
.card{background:#FFE8CC;border:1.5px solid #E8590C;border-radius:10px;padding:10px 12px;min-height:118px}
.card h2{font-size:15px;margin:0 0 6px;color:#D9480F}
.kv{display:flex;justify-content:space-between;gap:8px;border-top:1px dashed #F5C99B;padding:3px 0}
.kv:first-of-type{border-top:0}
.mono{font-family:Menlo,Consolas,monospace}
.neg{color:#C92A2A;font-weight:bold}
.flash{animation:f 1s}
@keyframes f{from{background:#FFF3BF}to{background:transparent}}
#log{margin:14px 0;padding:0;list-style:none}
#log li{background:#fff;border-left:4px solid #E8590C;border-radius:6px;padding:8px 12px;margin-bottom:8px}
#log li.bad{border-color:#C92A2A;background:#FFE3E3}
#log li.good{border-color:#F08C00;background:#FFF3BF}
#ask{font-weight:bold;margin:6px 0}
.btns{display:flex;flex-wrap:wrap;gap:10px}
button{font:inherit;cursor:pointer;border:1.5px solid #E8590C;background:#E8590C;color:#fff;border-radius:8px;padding:9px 16px}
button.alt{background:#fff;color:#D9480F}
button.danger{background:#C92A2A;border-color:#C92A2A}
button:focus-visible{outline:3px solid #F08C00;outline-offset:2px}
</style></head><body>
<h1>互动实验：DB 报错后，回补还是先查库？</h1>
<p class="lead">100 元（10000 分）红包拆成 3000、5000、2000 三份，钱已经进了托管账户。一步一步点，看 Redis 和 MySQL 两边怎么变。</p>
<div class="grid">
  <div class="card"><h2>Redis</h2>
    <div class="kv"><span>amounts (LIST)</span><span class="mono" id="list"></span></div>
    <div class="kv"><span>claimed (HASH)</span><span class="mono" id="hash"></span></div></div>
  <div class="card"><h2>MySQL 领取记录</h2><div id="claims"></div></div>
  <div class="card"><h2>MySQL 资金</h2>
    <div class="kv"><span>托管 redpacket_escrow</span><span class="mono" id="escrow"></span></div>
    <div class="kv"><span>已领合计 SUM(claim)</span><span class="mono" id="sum"></span></div>
    <div class="kv"><span>红包总额 Total</span><span class="mono">10000</span></div></div>
</div>
<ul id="log" aria-live="polite"></ul>
<p id="ask"></p>
<div class="btns" id="btns"></div>
<script>
var S;
function $(id){return document.getElementById(id)}
function reset(){
  S={list:[3000,5000,2000],hash:{},claims:[],escrow:10000,unknown:false};
  $('log').innerHTML='';
  step('用户 A 点开红包。','',[['① 用户 A 抢包（claim Lua）',grab]]);
}
function draw(){
  $('list').textContent=S.list.length?'['+S.list.join(', ')+']':'（空，已被删除）';
  var h=Object.keys(S.hash).map(function(k){return k+' → '+S.hash[k]});
  $('hash').textContent=h.length?h.join('；'):'（空）';
  $('claims').innerHTML=S.unknown?'<div class="kv"><span>A 的这笔</span><span>结果未知</span></div>':
    (S.claims.length?S.claims.map(function(c){return '<div class="kv"><span>用户 '+c[0]+'</span><span class="mono">'+c[1]+'</span></div>'}).join(''):'<div class="kv"><span>（还没有记录）</span></div>');
  var sum=S.claims.reduce(function(a,c){return a+c[1]},0);
  $('escrow').textContent=S.unknown?'结果未知':S.escrow;
  $('escrow').className='mono'+(S.escrow<0?' neg':'');
  $('sum').textContent=S.unknown?'结果未知':sum;
  $('sum').className='mono'+(sum>10000?' neg':'');
  ['list','hash','escrow','sum'].forEach(function(id){var e=$(id);e.classList.remove('flash');void e.offsetWidth;e.classList.add('flash')});
}
function step(msg,cls,choices,ask){
  if(msg){var li=document.createElement('li');li.className=cls||'';li.textContent=msg;$('log').appendChild(li);li.scrollIntoView({block:'nearest'})}
  $('ask').textContent=ask||'';
  var b=$('btns');b.innerHTML='';
  choices.forEach(function(c){var x=document.createElement('button');x.textContent=c[0];x.className=c[2]||'';x.onclick=c[1];b.appendChild(x)});
  draw();
}
var again=['重来',reset,'alt'];
function grab(){
  S.list.shift();S.hash.A='3000:1730680000';
  step('Redis：LPOP 弹出 3000，claimed 里记下 A → 3000:时间。A 拿到的只是一张号码牌，钱还没动。','',[['② 执行 DB 事务 claimTx',tx]]);
}
function tx(){
  S.unknown=true;
  step('DB 事务返回错误：driver: bad connection。Go 进程只知道“报错了”，不知道 MySQL 里到底提交没有。','bad',
    [['无脑回补：报错就把 3000 塞回 Redis',naive1,'danger'],['先查库：GetClaim(id, uid)',check]],'③ 现在怎么处理？');
}
function naive1(){
  S.unknown=false;S.claims.push(['A',3000]);S.escrow=7000;S.list.unshift(3000);delete S.hash.A;
  step('真相：MySQL 其实已经 COMMIT，A 钱包 +3000，托管剩 7000；只是确认包在路上丢了。无脑回补又把这 3000 LPUSH 回 LIST，还删了 A 的标记。','bad',
    [['下一步：用户 B 来抢',naive2]]);
}
function naive2(){
  S.list.shift();S.hash.B='3000:1730680001';S.claims.push(['B',3000]);S.escrow=4000;
  step('B 抢到的还是那 3000，DB 事务 COMMIT，B 钱包 +3000。同一份 3000 入账了两次，托管只剩 4000，可 LIST 里还有 5000 和 2000。','bad',
    [['下一步：剩下两份也被抢走',naive3]]);
}
function naive3(){
  S.list=[];S.hash.C='5000:1730680002';S.hash.D='2000:1730680003';S.claims.push(['C',5000],['D',2000]);S.escrow=-3000;
  step('C 领 5000、D 领 2000。已领合计 13000，超过红包总额 10000；托管余额 = 10000 - 13000 = -3000，托管池被击穿。报错不等于没提交。','bad',[again]);
}
function check(){
  step('不动 Redis，先反查这条领取记录。','',[
    ['分支 1：反查本身也报错',function(){branch(1)},'alt'],
    ['分支 2：库里已有这条记录',function(){branch(2)},'alt'],
    ['分支 3：没记录，txErr 是 errRedPacketClosed',function(){branch(3)},'alt'],
    ['分支 4：没记录，红包还在进行中',function(){branch(4)},'alt']],'④ 反查的结果是哪一种？');
}
function branch(n){
  S.unknown=false;
  var more=[['换一个分支看看',function(){S.unknown=true;S.list=[5000,2000];S.hash={A:'3000:1730680000'};S.claims=[];S.escrow=10000;check()},'alt'],again];
  if(n===1){S.unknown=true;
    step('分支 1：数据库彻底连不上，什么都不知道，就什么都不动。不回补，保留 claimed 标记，交给后台对账兜底；本次返回 txErr。','good',more)}
  if(n===2){S.claims=[['A',3000]];S.escrow=7000;
    step('分支 2：假失败。库里已经有 A 的 3000，说明事务其实提交了。按成功返回库里的金额，不回补。托管 7000，账是平的。','good',more)}
  if(n===3){S.escrow=0;S.list=[];
    step('分支 3：红包已结束。过期退款事务先拿到了行锁，剩余的钱已经按 DB 退给发包人（托管归零，Redis 剩余份额已清）。这一份不能再放回去给别人抢，不回补；本次返回失败。','good',more)}
  if(n===4){S.list=[3000,5000,2000];S.hash={};
    step('分支 4：确认未落库且红包进行中，这时才执行回补 Lua：LPUSH 3000 放回 LIST，HDEL 删掉 A 的标记，A 可以再点一次重抢。四个分支里只有这一个会回补。','good',more)}
}
reset();
</script></body></html>'''

SCENES.insert(LAB_AT, dict(type='interactive', title='互动实验：DB 报错后，回补还是先查库？',
                           content=dict(type='interactive', html=LAB_HTML)))

# =====================================================================
# 讲稿切分：同一段讲稿可以跨多页，按 cue 的起点切，拼回去必须和原文一字不差
# =====================================================================
by_src = {}
for sl in SLIDES:
    if sl.src is not None:
        by_src.setdefault(sl.src, []).append(sl)
assert sorted(by_src) == list(range(34)), '每段讲稿都要有页面认领'
speeches = {}
for src, slides in by_src.items():
    text, cues = SCRIPTS[src], [(sl, c) for sl in slides for c in sl.cues]
    pos = []
    for k, (sl, (_, start, _)) in enumerate(cues):
        p = 0 if k == 0 and not start else text.find(start, pos[-1] + 1 if pos else 0)
        assert start or k == 0, f'第 {sl.n} 页第 {k + 1} 个 cue 缺起点'
        assert p >= 0, f'第 {sl.n} 页：讲稿里找不到“{start}”'
        pos.append(p)
    assert pos[0] == 0, f'第 {slides[0].n} 页：第一句必须从讲稿开头起'
    segs = [text[a:b] for a, b in zip(pos, pos[1:] + [len(text)])]
    assert ''.join(segs) == text and all(segs)
    for (sl, _), seg in zip(cues, segs):
        speeches.setdefault(sl.n, []).append(seg)

scenes = []
for order, sc in enumerate(SCENES):
    scenes.append(sc.scene(order, speeches.get(sc.n, [])) if isinstance(sc, Slide) else {**sc, 'order': order})

manifest = dict(
    formatVersion=1,
    exportedAt=EXPORTED_AT,
    appVersion='1.5.0',
    stage=dict(name='18 红包：Redis 分发与 DB 记账的一致性',
               description='gomall 红包模块：Redis 管争抢，MySQL 管钱，两边对不上的时候怎么办。',
               language='zh-CN', createdAt=CREATED_AT, updatedAt=CREATED_AT),
    agents=[dict(name='老师', role='teacher',
                 persona='后端课程讲师，讲 gomall 红包模块。照着每页讲稿讲，先说现象再说原因，数字和代码位置以讲义为准。',
                 avatar='/avatars/teacher.png', color=O1, priority=10)],
    scenes=scenes,
    mediaIndex={},
)

# =====================================================================
# 自检
# =====================================================================
ACTION_REQUIRED = {'speech': ['text'], 'spotlight': ['elementId'], 'laser': ['elementId']}
ELEMENT_REQUIRED = {
    'text': ['content', 'defaultFontName', 'defaultColor', 'height', 'rotate'],
    'shape': ['viewBox', 'path', 'fixedRatio', 'fill', 'height', 'rotate'],
    'line': ['start', 'end', 'style', 'color', 'points'],
    'code': ['language', 'lines', 'height', 'rotate'],
}


def self_check(m):
    m = json.loads(json.dumps(m, ensure_ascii=False))   # 确认能序列化成合法 JSON
    assert m['formatVersion'] == 1 and m['mediaIndex'] == {} and len(m['agents']) == 1
    assert isinstance(m['stage']['name'], str) and isinstance(m['stage']['createdAt'], int)
    count = {}
    for i, sc in enumerate(m['scenes']):
        where = f'场景 {i}（{sc["title"]}）'
        assert sc['type'] == sc['content']['type'], where
        assert isinstance(sc['title'], str) and sc['title'] and sc['order'] == i, where
        count[sc['type']] = count.get(sc['type'], 0) + 1
        ids = set()
        if sc['type'] == 'slide':
            cv = sc['content']['canvas']
            assert cv['viewportSize'] == 1000 and cv['viewportRatio'] == 0.5625, where
            for el in cv['elements']:
                assert isinstance(el['id'], str) and el['id'] not in ids, f'{where} 元素 id 重复 {el["id"]}'
                ids.add(el['id'])
                for k in ['left', 'top', 'width'] + ELEMENT_REQUIRED[el['type']]:
                    assert k in el, f'{where} {el["id"]} 缺 {k}'
                if el['type'] == 'line':   # 线没有 height / rotate，范围按两个端点和拐点算
                    assert 'height' not in el and 'rotate' not in el, where
                    pts = [el['start'], el['end']] + ([el['broken']] if 'broken' in el else [])
                    xs = [el['left'] + p[0] for p in pts]
                    ys = [el['top'] + p[1] for p in pts]
                else:
                    xs = [el['left'], el['left'] + el['width']]
                    ys = [el['top'], el['top'] + el['height']]
                assert min(xs) >= 0 and max(xs) <= W and min(ys) >= 0 and max(ys) <= H, \
                    f'{where} {el["id"]} 出画布：x {min(xs)}..{max(xs)} y {min(ys)}..{max(ys)}'
                for fs in re.findall(r'font-size:(\d+)px', json.dumps(el)):
                    assert int(fs) >= 14, f'{where} {el["id"]} 字号 {fs}px 小于 14'
                if el['type'] == 'shape' and 'text' in el:
                    assert {'content', 'defaultFontName', 'defaultColor', 'align'} <= set(el['text']), where
            if 'actions' in sc and sc['actions']:
                n = sum(a['type'] == 'speech' for a in sc['actions'])
                assert 3 <= n <= 6, f'{where} 有 {n} 条 speech'
        elif sc['type'] == 'quiz':
            qs = sc['content']['questions']
            assert 2 <= len(qs) <= 3, where
            for q in qs:
                assert q['type'] in ('single', 'multiple') and q['analysis'] and q['answer'], where
                assert set(q['answer']) <= {o['value'] for o in q['options']}, where
                assert (len(q['answer']) == 1) == (q['type'] == 'single'), where
        else:
            doc = sc['content']['html']
            assert doc.startswith('<!doctype html>'), where
            assert not re.search(r'https?://|src=|href=|fetch\(|XMLHttpRequest|@import', doc), f'{where} 有外部依赖'
        seen = set()
        for a in sc.get('actions', []):
            assert isinstance(a['id'], str) and isinstance(a['type'], str) and a['id'] not in seen, where
            seen.add(a['id'])
            assert a['type'] in ACTION_REQUIRED, f'{where} 不该出现 {a["type"]}'
            for k in ACTION_REQUIRED[a['type']]:
                assert isinstance(a[k], str) and a[k], where
            if 'elementId' in a:
                assert a['elementId'] in ids, f'{where} 动作 {a["id"]} 指向不存在的元素 {a["elementId"]}'
    return count


if __name__ == '__main__':
    count = self_check(manifest)
    data = json.dumps(manifest, ensure_ascii=False, indent=1) + '\n'
    with open(OUT_JSON, 'w', encoding='utf-8') as f:
        f.write(data)
    with zipfile.ZipFile(OUT_ZIP, 'w', zipfile.ZIP_DEFLATED) as z:
        info = zipfile.ZipInfo('manifest.json', date_time=(2026, 11, 23, 0, 0, 0))
        info.compress_type = zipfile.ZIP_DEFLATED
        z.writestr(info, data)
    with zipfile.ZipFile(OUT_ZIP) as z:
        assert z.namelist() == ['manifest.json'] and json.loads(z.read('manifest.json')) == json.loads(data)
    acts = [a for sc in scenes for a in sc.get('actions', [])]
    print('场景', len(scenes), count, '| 元素', sum(len(sc['content']['canvas']['elements']) for sc in scenes
                                                if sc['type'] == 'slide'),
          '| speech', sum(a['type'] == 'speech' for a in acts),
          '| spotlight', sum(a['type'] == 'spotlight' for a in acts),
          '| laser', sum(a['type'] == 'laser' for a in acts))
    print('自检通过；讲稿 34 段逐字对上。')
