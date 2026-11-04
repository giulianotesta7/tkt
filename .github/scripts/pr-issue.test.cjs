const { test } = require('node:test');
const assert = require('node:assert/strict');
const { readFileSync } = require('node:fs');
const { join } = require('node:path');
const validate = require('./pr-issue.cjs');

test('workflow executes only trusted base code for PR events', () => {
  const workflow = readFileSync(join(__dirname, '../workflows/pr-issue.yml'), 'utf8');
  assert.match(workflow, /^  pull_request_target:\s*$/m);
  assert.doesNotMatch(workflow, /^  pull_request:\s*$/m);
  assert.match(workflow, /ref: \$\{\{ github\.event\.pull_request\.base\.sha \}\}/);
  assert.doesNotMatch(workflow, /github\.event\.pull_request\.head/);
  assert.equal((workflow.match(/uses: actions\/checkout@/g) || []).length, 1);
});

function fixture(body, resources = {}) {
  const calls = [];
  const github = { rest: { issues: { get: async (args) => {
    calls.push(args);
    const result = resources[args.issue_number];
    if (result instanceof Error) throw result;
    if (!result) throw Object.assign(new Error('Not Found'), { status: 404 });
    return { data: result };
  } } } };
  const context = { repo: { owner: 'owner', repo: 'repo' }, payload: {
    pull_request: { body, labels: [], assignees: [] },
  } };
  return { run: () => validate({ github, context }), calls };
}

test('missing, placeholder, unrelated and malformed references fail without API calls', async () => {
  for (const body of [null, '', 'Closes #', 'See #123', 'Closes #0', 'Closes #-1',
    'Closes #12abc', 'Closes #1.5', 'Closes #9007199254740992', '<!-- Closes #123 -->',
    '<!--\nCloses #123', 'Text Closes #123', 'Closes #123 extra']) {
    const f = fixture(body);
    await assert.rejects(f.run(), /Closes #<number>/);
    assert.deepEqual(f.calls, []);
  }
});

test('a real issue passes with no approval labels or assignees', async () => {
  const f = fixture('## Related issue\n\nCloses #123', { 123: { number: 123 } });
  assert.equal(await f.run(), 123);
  assert.deepEqual(f.calls, [{ owner: 'owner', repo: 'repo', issue_number: 123 }]);
});

test('a PR reference is not an issue', async () => {
  await assert.rejects(fixture('Closes #123', { 123: { pull_request: {} } }).run(), /existing issue/);
});

test('a missing issue fails and API errors are not accepted', async () => {
  await assert.rejects(fixture('Closes #123').run(), /existing issue/);
  await assert.rejects(fixture('Closes #123', { 123: new Error('API unavailable') }).run(), /API unavailable/);
});

test('at least one valid issue suffices; repeated references are deduplicated', async () => {
  const f = fixture('Closes #123\nCloses #123\nCloses #456', { 456: { number: 456 } });
  assert.equal(await f.run(), 456);
  assert.deepEqual(f.calls.map(call => call.issue_number), [123, 456]);
});

test('a PR reference is skipped when a later reference is an issue', async () => {
  const f = fixture('Closes #123\nCloses #456', { 123: { pull_request: {} }, 456: { number: 456 } });
  assert.equal(await f.run(), 456);
  assert.deepEqual(f.calls.map(call => call.issue_number), [123, 456]);
});

test('editing the body to add or remove a reference changes the result', async () => {
  await assert.rejects(fixture('## Related issue').run());
  assert.equal(await fixture('Closes #123', { 123: { number: 123 } }).run(), 123);
  await assert.rejects(fixture('Closes #').run());
});
