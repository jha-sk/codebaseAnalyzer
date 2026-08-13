function fetchThing() {
  return Promise.resolve({ json: () => ({ ok: true }) });
}

// Floating promise: no return, no .catch() - trips promise/catch-or-return.
fetchThing().then(r => r.json());

// eval on unsanitized input - trips no-eval.
function runUserInput(userInput) {
  eval(userInput);
}

// Reference to a variable that is never declared or imported - trips no-undef.
console.log(undeclaredVariable);

module.exports = { runUserInput };
