import http from 'k6/http';
import { Counter } from 'k6/metrics';
import { check } from 'k6';

const BATCH_SIZE = Number(__ENV.BATCH_SIZE || 100);
const scenarioName = __ENV.SCENARIO || 'random_spikes';
const batchWrites = new Counter('batch_writes');
const payload = JSON.stringify(
    Array.from({ length: BATCH_SIZE }, () => ({
        name: 'Gourav',
        message: 'Benchmark test',
    }))
);
const highRate = Number(__ENV.HIGH_RATE || 1000);
const randomSpikeStages = Array.from({ length: 8 }, () => ({
    duration: `${Math.floor(Math.random() * 4) + 2}s`,
    target: Math.floor(Math.random() * 1800) + 200,
}));

const scenarios = {
    random_spikes: {
        executor: 'ramping-arrival-rate',
        startRate: 50,
        timeUnit: '1s',
        preAllocatedVUs: 100,
        maxVUs: 2000,
        stages: randomSpikeStages,
        exec: 'randomSpikes',
    },
    constant_high: {
        executor: 'constant-arrival-rate',
        rate: highRate,
        timeUnit: '1s',
        duration: '30s',
        preAllocatedVUs: 100,
        maxVUs: 2000,
        exec: 'constantHigh',
    },
};

if (!scenarios[scenarioName]) {
    throw new Error(`Unknown SCENARIO: ${scenarioName}`);
}

export const options = {
    scenarios: {
        [scenarioName]: scenarios[scenarioName],
    },
};

function submitBatch() {
    batchWrites.add(BATCH_SIZE);
    const res = http.post(
        'http://localhost:3000/submit/batch',
        payload,
        {
            headers: {
                'Content-Type': 'application/json',
            },
        }
    );

    check(res, {
        'status is 201': (r) => r.status === 201,
    });
}

export function randomSpikes() {
    submitBatch();
}

export function constantHigh() {
    submitBatch();
}
