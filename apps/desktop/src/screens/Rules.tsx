import * as api from "../api";
import { agents, errorMessage, rules, servers } from "../state";
import { relative } from "../time";
import type { Rule } from "../types";
import { Button, Chip, Empty, Label, describeError } from "../ui";

function nameOf(list: { id: string; name: string }[], id: string | null): string {
  if (!id) return "any";
  return list.find((x) => x.id === id)?.name ?? id;
}

function scopeChip(rule: Rule) {
  return rule.scope === "session" ? <Chip tone="warn">this session</Chip> : <Chip>always</Chip>;
}

export function RulesScreen() {
  const list = rules.value;
  const remove = async (id: string) => {
    try {
      await api.deleteRule(id);
      rules.value = await api.listRules();
    } catch (err) {
      errorMessage.value = describeError(err);
    }
  };

  return (
    <div>
      <Label right={<span>{list.length}</span>}>Rules</Label>
      {list.length === 0 ? (
        <Empty title="No rules yet.">Answer a held call with “for this session” or “always” and the rule lands here. Session rules vanish when Prism quits.</Empty>
      ) : (
        <div class="list">
          {list.map((rule) => (
            <div class="item" key={rule.id}>
              <div class="title">
                <Chip tone={rule.decision === "allow" ? "ok" : "danger"}>{rule.decision}</Chip>
                {scopeChip(rule)}
              </div>
              <div class="side">
                <Button variant="quiet" class="danger" onClick={() => void remove(rule.id)}>
                  Delete
                </Button>
              </div>
              <div class="sub truncate">
                {nameOf(agents.value, rule.agent_id)} · {nameOf(servers.value, rule.server_id)} · {rule.tool ?? "any tool"} · {relative(rule.created_at)}
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
