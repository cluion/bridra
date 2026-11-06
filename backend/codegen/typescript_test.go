package codegen

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func webTestSchema(t *testing.T) Schema {
	t.Helper()
	schema, err := LoadSchema("../../schema/bridra.json")
	if err != nil {
		t.Fatal(err)
	}
	return schema
}

func TestWebClientRejectsUnsupportedContracts(t *testing.T) {
	for name, edit := range map[string]func(*Schema){
		"streaming method":          func(s *Schema) { s.Methods[1].Stream = true },
		"file field":                func(s *Schema) { s.Methods[1].Params.Fields = []Field{{Name: "file", Type: "file"}} },
		"requires system.health":    func(s *Schema) { s.Methods[0].Name = "demo.check" },
		"conflicts with TypeScript": func(s *Schema) { s.Methods[1].ClientName = "then" },
	} {
		t.Run(name, func(t *testing.T) {
			s := webTestSchema(t)
			edit(&s)
			outputs, err := GenerateWithOptions(s, Options{TypeScript: true})
			if err == nil || !strings.Contains(err.Error(), name) || len(outputs) != 0 {
				t.Fatalf("expected rejection %q, got %v and %d outputs", name, err, len(outputs))
			}
		})
	}
}

func TestWebClientCallsSameOriginAndValidatesReplies(t *testing.T) {
	tsc, err := exec.LookPath("tsc")
	if err != nil {
		t.Skip("requires TypeScript compiler")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("requires Node.js")
	}
	s := webTestSchema(t)
	s.Methods = append(s.Methods, Method{Name: "example.read", ClientName: "read", Meta: []Field{{Name: "toString", Type: "string", Nullable: true}}, Result: Object{
		GoType: "ReadResponse", DartType: "ReadResult", Fields: []Field{
			{Name: "items", Type: "object", Array: true, Nullable: true, Object: &Object{GoType: "ItemResponse", DartType: "Item", Fields: []Field{
				{Name: "id", Type: "integer"}, {Name: "state", Type: "string", Enum: []string{"yes", "no"}},
			}}},
		},
	}})
	outputs, err := GenerateWithOptions(s, Options{TypeScript: true})
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := Write(root, outputs); err != nil {
		t.Fatal(err)
	}
	compiled := filepath.Join(root, "compiled")
	cmd := exec.Command(tsc, "--strict", "--noUnusedLocals", "--noUnusedParameters", "--target", "ES2022", "--module", "commonjs", "--lib", "ES2022,DOM", "--outDir", compiled, TypeScriptClientPath)
	cmd.Dir = root
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("tsc: %s: %v", b, err)
	}
	cmd = exec.Command(node, "-e", `
const assert = require("node:assert/strict");
const { BridraClient, HttpError, RpcError } = require("./bridra.generated.js");
const client = new BridraClient();
const health = {status:"ok", frameworkVersion:"v", protocolVersion:1, runtime:"go", architecture:"arm64"};
let mode = "ok", calls = [];
globalThis.fetch = async (url, request) => {
  assert.equal(url,"/rpc"); assert.equal(request.credentials,"same-origin"); assert.equal(request.mode,"same-origin");
  assert.equal(request.redirect,"error"); assert.ok(!("authorization" in request.headers));
  const {id, method} = JSON.parse(request.body); calls.push(method);
  if (mode === "http") return new Response(null,{status:401});
  if (mode === "rpc") return Response.json({id,error:{code:"invalid_params",message:"bad"}});
  if (method === "system.health") return Response.json({id:mode==="wrong-id" ? "wrong" : id,result:{...health,protocolVersion:mode==="version" ? 9 : 1}});
  return Response.json({id,result:{items:[{id:3,state:mode==="bad-dto" ? "other" : "yes"}]}});
};
(async () => {
  assert.deepEqual(await client.read(), {items:[{id:3,state:"yes"}], toString: null});
  assert.deepEqual(calls, ["system.health","example.read"]);
  mode="bad-dto"; await assert.rejects(client.read(),/Unexpected value/);
  mode="version"; await assert.rejects(client.health(),/Protocol mismatch/);
  calls=[]; await assert.rejects(client.read(),/Protocol mismatch/); assert.deepEqual(calls,["system.health"]);
  mode="wrong-id"; await assert.rejects(client.health(),/Mismatched response ID/);
  mode="http"; await assert.rejects(client.health(), e=>e instanceof HttpError && e.status===401);
  mode="rpc"; await assert.rejects(client.health(), e=>e instanceof RpcError && e.code==="invalid_params");
})().catch(e=>{console.error(e);process.exitCode=1;});`)
	cmd.Dir = compiled
	if err := os.WriteFile(filepath.Join(compiled, "package.json"), []byte(`{"type":"commonjs"}`), 0644); err != nil {
		t.Fatal(err)
	}
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("client checks: %s: %v", b, err)
	}
}
