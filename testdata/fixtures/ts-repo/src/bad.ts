import { exec } from "child_process";

// Type error: assigning a string to a number - trips tsc's strict type check.
const count: number = "not a number";

function runForUser(userInput: string): void {
  // Command built from a template string, not a string literal - trips
  // security/detect-child-process.
  exec(`ls ${userInput}`, (_err, stdout) => {
    console.log(stdout, count);
  });
}

runForUser(process.argv[2] ?? "");
