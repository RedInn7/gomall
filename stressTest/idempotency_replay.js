// 幂等中间件：同一 token 重复请求只能成功一次，后续请求走缓存回放。
// VU 在 setup 阶段共享拿一个 token，运行时所有人都用它打 /orders/create。
// 期望：1 个成功，剩余几千个全部命中缓存（X-Idempotent-Replay: true 或同 body）
//
// 跑法: ADDRESS_ID=<压测账号的地址 id> PRODUCT_ID=<在售商品 id> k6 run stressTest/idempotency_replay.js
import http from 'k6/http';
import { check } from 'k6';

const config = JSON.parse(open('./config.json'));
const BASE = __ENV.BASE_URL || 'http://localhost:5003/api/v1';
// 下单要用压测账号自己的收货地址和一个有库存的在售商品，否则每次都在业务校验处失败、一单也落不了
const PRODUCT_ID = Number(__ENV.PRODUCT_ID || 2);
const ADDRESS_ID = Number(__ENV.ADDRESS_ID || 0);

export const options = {
    vus: 50,
    duration: '15s',
};

export function setup() {
    if (!ADDRESS_ID) throw new Error('set ADDRESS_ID to an address owned by the test user');
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
        JSON.stringify({ product_id: PRODUCT_ID, num: 1, address_id: ADDRESS_ID }),
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
        'not rate-limited': r => !r.body || r.body.indexOf('"status":70001') < 0,
        'no duplicate side-effect': r => r.body.indexOf('60002') < 0 || r.body.indexOf('"order_num"') >= 0,
    });
}
