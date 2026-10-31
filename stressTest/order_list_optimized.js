// 订单列表（优化版，游标分页 + 缓存）。
//
// 跑法: k6 run stressTest/order_list_optimized.js
import http from 'k6/http';
import { check } from 'k6';

const config = JSON.parse(open('./config.json'));
const BASE = __ENV.BASE_URL || 'http://localhost:5003/api/v1';

export const options = {
    vus: 100,
    duration: '30s',
    thresholds: {
        'http_req_duration': ['p(95)<300'],
    },
};

export default function () {
    const res = http.get(
        `${BASE}/orders/list?last_id=1999999&type=2`,
        { headers: { access_token: config.access_token, refresh_token: config.refresh_token } },
    );
    check(res, {
        'status 200': r => r.status === 200,
        'not rate-limited': r => !r.body || r.body.indexOf('"status":70001') < 0,
    });
}
