// 优惠券领取（Redis Lua 原子模式）。
// setup 阶段创建一个新批次，运行时所有 VU 抢这批次。
// 期望：总成功数 = 批次 total（不超发）；剩余请求按 PerUser 配额命中"超限"。
//
// 跑法: k6 run stressTest/coupon_claim_redis.js
import http from 'k6/http';
import { check } from 'k6';
import { Counter } from 'k6/metrics';

const config = JSON.parse(open('./config.json'));
const BASE = 'http://localhost:5002/api/v1';

export const options = {
    vus: 80,
    duration: '20s',
};

const success = new Counter('coupon_success');
const failed = new Counter('coupon_failed');

export function setup() {
    const now = new Date();
    const end = new Date(now.getTime() + 3600 * 1000);
    const r = http.post(
        `${BASE}/coupon/batch`,
        JSON.stringify({
            name: 'k6-redis-' + now.getTime(),
            type: 1,
            threshold: 0,
            amount: 100,
            total: 500,
            per_user: 1,
            start_at: now.toISOString(),
            end_at: end.toISOString(),
            valid_days: 1,
        }),
        {
            headers: {
                'Content-Type': 'application/json',
                access_token: config.access_token,
                refresh_token: config.refresh_token,
            },
        },
    );
    if (r.status !== 200) throw new Error(`create batch failed: ${r.status} ${r.body}`);
    const body = r.json();
    const id = body.data.ID || body.data.id;
    return { batchId: id };
}

export default function (data) {
    const res = http.post(
        `${BASE}/coupon/claim`,
        JSON.stringify({ batch_id: data.batchId, mode: 'redis' }),
        {
            headers: {
                'Content-Type': 'application/json',
                access_token: config.access_token,
                refresh_token: config.refresh_token,
            },
        },
    );
    if (res.status === 200 && res.body.indexOf('"Code"') >= 0) {
        success.add(1);
    } else {
        failed.add(1);
    }
    check(res, { 'http 200': r => r.status === 200 });
}
