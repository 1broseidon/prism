// mcpURLFromBase derives the MCP connection URL agents use to reach the
// gateway. The base is the operator-pinned gateway public URL; the path
// is always /mcp.
export function mcpURLFromBase(base: string): string {
  const trimmed = base.trim().replace(/\/+$/, "");
  if (!trimmed) return "";
  return trimmed + "/mcp";
}
