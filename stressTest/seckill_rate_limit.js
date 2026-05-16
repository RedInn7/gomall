// 秒杀接口 + 滑动窗口限流：单用户 1s 内最多 3 次。
// 我们用 1 个用户 token 高并发打，期望大部分请求被 70001 拦下。
//
// 跑法: k6 run stressTest/seckill_rate_limit.js
import http from 'k6/http';
import { check } from 'k6';
import { Counter } from 'k6/metrics';

const config = JSON.parse(open('./config.json'));

export const options = {
    vus: 30,
    duration: '15s',
};

const passed = new Counter('seckill_passed');
const limited = new Counter('seckill_limited');

export default function () {
    const res = http.post(
        'http://localhost:5002/api/v1/skill_product/skill',
        JSON.stringify({ product_id: 1 }),
        {
            headers: {
                'Content-Type': 'application/json',
                access_token: config.access_token,
                refresh_token: config.refresh_token,
            },
        },
    );
    const body = res.body || '';
    if (body.indexOf('70001') >= 0) {
        limited.add(1);
    } else {
        passed.add(1);
    }
    check(res, { 'http 200': r => r.status === 200 });
}
