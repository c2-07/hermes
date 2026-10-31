import http from 'k6/http';
import { check } from 'k6';

const BASE_URL = 'https://cautious-winner-699gjppxgvrf5pjx-3000.app.github.dev';

export const options = {
    scenarios: {
        benchmark: {
            executor: 'constant-vus',
            vus: 100,
            duration: '30s',
        },
    },
    thresholds: {
        http_req_failed: ['rate<0.01'],
        http_req_duration: ['p(95)<1000'],
    },
};

export default function () {
    const res = http.get(`${BASE_URL}/health`);

    check(res, {
        'status is 200': (r) => r.status === 200,
    });
}
