module.exports = async function validate({ github, context }) {
  const body = (context.payload.pull_request.body || '').replace(/<!--[\s\S]*?(?:-->|$)/g, '');
  const numbers = new Set();
  for (const line of body.split(/\r?\n/)) {
    const match = line.match(/^\s*Closes #([0-9]+)\s*$/i);
    if (!match) continue;
    const number = Number(match[1]);
    if (Number.isSafeInteger(number) && number > 0) numbers.add(number);
  }

  if (!numbers.size) throw new Error('Add a standalone Closes #<number> line linking an issue.');
  for (const issue_number of numbers) {
    try {
      const { data } = await github.rest.issues.get({ ...context.repo, issue_number });
      if (!data.pull_request) return issue_number;
    } catch (error) {
      if (error.status !== 404) throw error;
    }
  }
  throw new Error('Closes #<number> must link an existing issue in this repository.');
};
