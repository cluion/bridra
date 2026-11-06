import { useState } from "react";
import { createRoot } from "react-dom/client";
import { createActions, resultText } from "./actions";
import "./style.css";

function App() {
  const [actions] = useState(createActions);
  const [name, setName] = useState("");
  const [output, setOutput] = useState("");
  const [busy, setBusy] = useState(false);

  async function show(action) {
    if (busy) return;
    setBusy(true);
    try {
      setOutput(await resultText(action));
    } finally {
      setBusy(false);
    }
  }

  return (
    <main>
      <h1>{document.title}</h1>
      <button data-testid="health" disabled={busy} onClick={() => show(actions.health)}>Check health</button>
      <label htmlFor="name">Name</label>
      <form onSubmit={event => { event.preventDefault(); show(() => actions.greet(name)); }}>
        <input id="name" data-testid="name" value={name} onChange={event => setName(event.target.value)} required maxLength={64} />
        <button data-testid="greet" disabled={busy}>Send greeting</button>
      </form>
      <pre data-testid="output" aria-live="polite">{output}</pre>
    </main>
  );
}

createRoot(document.getElementById("app")).render(<App />);
