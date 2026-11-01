// 秒杀接口 + 滑动窗口限流：单用户 1s 内最多 3 次。
// 我们用 1 个用户 token 高并发打，期望大部分请求被 70001 拦下。
// 全局令牌桶（每 IP 100 RPS）也返回 70001，data 为"请求过于频繁"，单独计数，不能算进滑动窗口的拦截。
// 限流在幂等中间件之前：这里不带 Idempotency-Key，放行的请求会被幂等中间件以 400 拒绝，只用来数限流。
//
// 跑法: k6 run stressTest/seckill_rate_limit.js
import http from 'k6/http';
import { check } from 'k6';
import { Counter } from 'k6/metrics';

const config = JSON.parse(open('./config.json'));
const BASE = __ENV.BASE_URL || 'http://localhost:5003/api/v1';

export const options = {
    vus: 30,
    duration: '15s',
};

const passed = new Counter('seckill_passed');
const limited = new Counter('seckill_limited');
const globalLimited = new Counter('global_limited');

export default function () {
    const res = http.post(
        `${BASE}/skill_product/skill`,
        JSON.stringify({ skill_product_id: 1, address_id: 1 }),
        {
            headers: {
                'Content-Type': 'application/json',
                access_token: config.access_token,
                refresh_token: config.refresh_token,
            },
        },
    );
    const body = res.body || '';
    const globalHit = body.indexOf('"data":"请求过于频繁"') >= 0;
    if (globalHit) {
        globalLimited.add(1);
    } else if (body.indexOf('"status":70001') >= 0) {
        limited.add(1);
    } else {
        passed.add(1);
    }
    check(res, {
        'http 200': r => r.status === 200,
        'not limited by global token bucket': () => !globalHit,
    });
}
