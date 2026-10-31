import http from 'k6/http';
import { check, sleep } from 'k6';

export const options = {
    stages: [
        { duration: '10s', target: 10 },
        { duration: '20s', target: 50 },
        { duration: '30s', target: 100 },
        { duration: '10s', target: 0 },
    ],
    thresholds: {
        http_req_failed: ['rate<0.01'],
        http_req_duration: ['p(95)<500'],
    },
};

export default function () {
    const payload = JSON.stringify({
        name: `User-${__VU}`,
        message: `Request from iteration ${__ITER}`,
    });

    const params = {
        headers: {
            'Content-Type': 'application/json',
        },
    };

    const res = http.post(
        'http://localhost:3000/submit',
        payload,
        params
    );

    check(res, {
        'status is 201': (r) => r.status === 201,
    });

    // sleep(0.1);
}
