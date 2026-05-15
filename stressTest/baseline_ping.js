// 基线：无业务逻辑、无 DB/Redis 的 /ping 接口。
// 用来给后面各链路的对比提供"裸 gin + 中间件链"的吞吐上限。
//
// 跑法: k6 run stressTest/baseline_ping.js
import http from 'k6/http';
import { check } from 'k6';

export const options = {
    vus: 100,
    duration: '30s',
    thresholds: {
        'http_req_duration': ['p(95)<50'],
        'http_req_failed': ['rate<0.01'],
    },
};

export default function () {
    const res = http.get('http://localhost:5002/api/v1/ping');
    check(res, { 'status 200': r => r.status === 200 });
}
