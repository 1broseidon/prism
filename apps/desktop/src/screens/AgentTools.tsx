import { useEffect, useState } from "preact/hooks";
import * as api from "../api";
import { ACCESS, findRule, postureLabel, setAccess } from "../policy";
import { agents, errorMessage, pop, rules, servers, status } from "../state";
import type { ToolInfo } from "../types";
import { Chip, Label, Pager, Screen, Segmented, describeError, usePage } from "../ui";

/** Rows per page: six two-line rows fit the body beside the pager. */
const PAGE = 6;

/** Per-tool overrides for one agent on one server. */
export function AgentToolsScreen({ agentId, serverId }: { agentId: string; serverId: string }) {
  const agent = agents.value.find((a) => a.id === agentId);
  const server = servers.value.find((s) => s.id === serverId);
  const [tools, setTools] = useState<ToolInfo[] | null>(null);

  useEffect(() => {
    api.listServerTools(serverId).then(setTools).catch((err) => {
      errorMessage.value = describeError(err);
    });
  }, [serverId]);

  const loaded = status.value !== null;
  useEffect(() => {
    if (loaded && (!agent || !server)) pop();
  }, [loaded, agent, server]);
  if (!agent || !server) return <div class="screen pushed" />;

  const serverRule = findRule(rules.value, agent.id, server.id, null);
  const fallback = serverRule ? serverRule.decision : postureLabel(agent.posture).toLowerCase();
  const { rows, offset, setOffset, total } = usePage(tools ?? [], PAGE, serverId);

  return (
    <div class="screen pushed">
      <Screen footer={total > PAGE ? <Pager offset={offset} size={PAGE} total={total} onOffset={setOffset} /> : undefined}>
        <Label right={<span>unset: {fallback}</span>}>Tools</Label>
        {tools === null ? null : tools.length === 0 ? (
          <p class="hint">No tools.</p>
        ) : (
          <div class="list">
            {rows.map((tool) => {
              const rule = findRule(rules.value, agent.id, server.id, tool.name);
              return (
                <div class="item" key={tool.name}>
                  <div class="title">
                    <span class="truncate mono small">{tool.name}</span>
                    {tool.read_only ? <Chip tone="ok">Read</Chip> : null}
                    {tool.destructive ? <Chip tone="warn">Writes</Chip> : null}
                  </div>
                  <div class="side">
                    <Segmented
                      small
                      label={`Access to ${tool.name}`}
                      value={rule?.decision ?? null}
                      options={ACCESS}
                      onChange={(next) => {
                        setAccess(agent.id, server.id, tool.name, rule?.decision === next ? null : next).catch((err) => {
                          errorMessage.value = describeError(err);
                        });
                      }}
                    />
                  </div>
                  {tool.description ? <div class="sub truncate" title={tool.description}>{tool.description}</div> : null}
                </div>
              );
            })}
          </div>
        )}
      </Screen>
    </div>
  );
}
