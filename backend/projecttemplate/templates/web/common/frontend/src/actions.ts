import { BridraClient } from "./bridra.generated";

export function createActions() {
  const client = new BridraClient();
  return {
    health: () => client.health(),
    greet: (name: string) => client.greet({ name }),
  };
}

export async function resultText(request: () => Promise<unknown>): Promise<string> {
  try {
    return JSON.stringify(await request(), null, 2);
  } catch (error) {
    const status = error instanceof Error && "status" in error ? error.status : undefined;
    const code = error instanceof Error && "code" in error ? error.code : undefined;
    const message = error instanceof Error ? error.message : String(error);
    return JSON.stringify({ status, code, message }, null, 2);
  }
}
