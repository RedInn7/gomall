import http from 'k6/http';
import { check, sleep } from 'k6';

const config = JSON.parse(open("./config.json"));

export const options = {
    vus: 100,
    duration: '30s',
};

export default function () {
    // 2. 从 config 对象中获取 Token
    const url = 'http://localhost:5002/api/v1/orders/old/list?last_id=1999999&type=2';

    const params = {
        headers: {
            'access_token': config.access_token,
            'refresh_token': config.refresh_token,
        },
    };

    let res = http.get(url, params);

    if (res.status !== 200 && __ITER === 0) {
        console.log(`❌ Error: ${res.status} | Body: ${res.body}`);
    }

    check(res, {
        'is status 200': (r) => r.status === 200,
    });

    sleep(0.1);
}