// 优惠券领取（DB 悲观锁模式）。
// 期望吞吐显著低于 Redis 模式 —— 同一行的 SELECT FOR UPDATE 会强制串行。
//
// 跑法: k6 run stressTest/coupon_claim_db.js
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
            name: 'k6-db-' + now.getTime(),
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
        JSON.stringify({ batch_id: data.batchId, mode: 'db' }),
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
