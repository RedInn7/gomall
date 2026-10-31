// 商品列表：测试 DB 读路径的吞吐。
// 关键观察：限流中间件挡不挡得住（默认 100 RPS/IP）→ 应当看到 70001 比例
//
// 跑法: k6 run stressTest/product_list.js
import http from 'k6/http';
import { check } from 'k6';

const BASE = __ENV.BASE_URL || 'http://localhost:5003/api/v1';

export const options = {
    vus: 50,
    duration: '30s',
    thresholds: {
        'http_req_duration': ['p(95)<500'],
    },
};

export default function () {
    const res = http.get(`${BASE}/product/list?page_num=1&page_size=20`);
    check(res, {
        'status 200': r => r.status === 200,
        'not rate-limited': r => !r.body || r.body.indexOf('"status":70001') < 0,
    });
}
