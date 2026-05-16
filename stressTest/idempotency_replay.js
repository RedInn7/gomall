// 幂等中间件：同一 token 重复请求只能成功一次，后续请求走缓存回放。
// VU 在 setup 阶段共享拿一个 token，运行时所有人都用它打 /orders/create。
// 期望：1 个成功，剩余几千个全部命中缓存（X-Idempotent-Replay: true 或同 body）
//
// 跑法: k6 run stressTest/idempotency_replay.js
import http from 'k6/http';
import { check } from 'k6';

const config = JSON.parse(open('./config.json'));
const BASE = 'http://localhost:5002/api/v1';

export const options = {
    vus: 50,
    duration: '15s',
};

export function setup() {
    const r = http.get(`${BASE}/idempotency/token`, {
        headers: { access_token: config.access_token, refresh_token: config.refresh_token },
    });
    if (r.status !== 200) {
        throw new Error(`get token failed: ${r.status} ${r.body}`);
    }
    const body = r.json();
    return { token: body.data.idempotency_key };
}

export default function (data) {
    const res = http.post(
        `${BASE}/orders/create`,
        JSON.stringify({ product_id: 1, boss_id: 2, num: 1, money: 100, address_id: 1 }),
        {
            headers: {
                'Content-Type': 'application/json',
                'access_token': config.access_token,
                'refresh_token': config.refresh_token,
                'Idempotency-Key': data.token,
            },
        },
    );
    check(res, {
        'status 200': r => r.status === 200,
        'no duplicate side-effect': r => r.body.indexOf('60002') < 0 || r.body.indexOf('"order_num"') >= 0,
    });
}
