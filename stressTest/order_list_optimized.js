// 订单列表（优化版，游标分页 + 缓存）。
// 对比 order_list_deep_pagination.js 能看出深分页/Redis 缓存的优化效果。
//
// 跑法: k6 run stressTest/order_list_optimized.js
import http from 'k6/http';
import { check } from 'k6';

const config = JSON.parse(open('./config.json'));

export const options = {
    vus: 100,
    duration: '30s',
    thresholds: {
        'http_req_duration': ['p(95)<300'],
    },
};

export default function () {
    const res = http.get(
        'http://localhost:5002/api/v1/orders/list?last_id=1999999&type=2',
        { headers: { access_token: config.access_token, refresh_token: config.refresh_token } },
    );
    check(res, { 'status 200': r => r.status === 200 });
}
