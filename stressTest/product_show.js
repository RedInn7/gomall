// 商品详情：随机命中商品 ID（1-100 之间），混合命中/未命中场景。
// 当前 main 上 ProductShow 直接查 DB，feat/product-cache-consistency 合并后会接 Redis。
// 跑两次对比能直观看到 Cache Aside 的收益。
//
// 跑法: k6 run stressTest/product_show.js
import http from 'k6/http';
import { check } from 'k6';

export const options = {
    vus: 80,
    duration: '30s',
    thresholds: {
        'http_req_duration': ['p(95)<400'],
    },
};

export default function () {
    const id = Math.floor(Math.random() * 100) + 1;
    const res = http.get(`http://localhost:5002/api/v1/product/show?id=${id}`);
    check(res, {
        'status 200': r => r.status === 200,
    });
}
