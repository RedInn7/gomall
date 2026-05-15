// 订单列表（旧版深分页）。
// PR #38 之前的实现，使用 OFFSET 翻页，在 6M 行级别的表上必然慢。
//
// 跑法: k6 run stressTest/order_list_deep_pagination.js
import http from 'k6/http';
import { check } from 'k6';

const config = JSON.parse(open('./config.json'));

export const options = {
    vus: 100,
    duration: '30s',
    thresholds: {
        'http_req_duration': ['p(95)<5000'],
    },
};

export default function () {
    const res = http.get(
        'http://localhost:5002/api/v1/orders/old/list?last_id=1999999&type=2',
        { headers: { access_token: config.access_token, refresh_token: config.refresh_token } },
    );
    check(res, { 'status 200': r => r.status === 200 });
}
