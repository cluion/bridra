<script setup>
import { ref } from "vue";
import { createActions, resultText } from "./actions";

const productName = document.title;
const actions = createActions();
const name = ref("");
const output = ref("");
const busy = ref(false);

async function show(action) {
  if (busy.value) return;
  busy.value = true;
  try {
    output.value = await resultText(action);
  } finally {
    busy.value = false;
  }
}
</script>

<template>
  <main>
    <h1 v-text="productName"></h1>
    <button data-testid="health" :disabled="busy" @click="show(actions.health)">Check health</button>
    <label for="name">Name</label>
    <form @submit.prevent="show(() => actions.greet(name))">
      <input id="name" data-testid="name" v-model="name" required maxlength="64">
      <button data-testid="greet" :disabled="busy">Send greeting</button>
    </form>
    <pre data-testid="output" aria-live="polite" v-text="output"></pre>
  </main>
</template>
