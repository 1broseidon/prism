import * as api from "../api";
import { agents, errorMessage, rules, servers } from "../state";
import { relative, remaining } from "../time";
import type { Rule } from "../types";
import { Chip, ConfirmButton, Empty, Label, Pager, Screen, describeError, usePage } from "../ui";

/** Rows per page: five chip-and-line rows fit under the tabs beside the pager. */
const PAGE = 5;

function nameOf(list: { id: string; name: string }[], id: string | null): string {
  if (!id) return "any";
  return list.find((x) => x.id === id)?.name ?? id;
}

function scopeChip(rule: Rule) {
  if (rule.expires_at) return <Chip tone="warn">{remaining(rule.expires_at)} left</Chip>;
  return rule.scope === "session" ? <Chip tone="warn">This session</Chip> : <Chip>Always</Chip>;
}

function sentence(value: string): string {
  return value[0].toUpperCase() + value.slice(1);
}

function decisionTone(rule: Rule): "ok" | "danger" | "warn" {
  return rule.decision === "allow" ? "ok" : rule.decision === "deny" ? "danger" : "warn";
}

export function RulesScreen() {
  const list = rules.value;
  const { rows, offset, setOffset, total } = usePage(list, PAGE);
  const remove = async (id: string) => {
    try {
      await api.deleteRule(id);
      rules.value = await api.listRules();
    } catch (err) {
      errorMessage.value = describeError(err);
    }
  };

  return (
    <div class="screen">
      <Screen footer={total > PAGE ? <Pager offset={offset} size={PAGE} total={total} onOffset={setOffset} /> : undefined}>
      <Label right={<span>{list.length}</span>}>Rules</Label>
      {list.length === 0 ? (
        <Empty title="No rules yet." />
      ) : (
        <div class="list">
          {rows.map((rule) => (
            <div class="item" key={rule.id}>
              <div class="title">
                <Chip tone={decisionTone(rule)}>{sentence(rule.decision)}</Chip>
                {scopeChip(rule)}
                {rule.attention ? <Chip tone="accent">{sentence(rule.attention)}</Chip> : null}
              </div>
              <div class="side">
                <ConfirmButton variant="quiet" class="danger" confirm="Delete?" onConfirm={() => void remove(rule.id)}>
                  Delete
                </ConfirmButton>
              </div>
              <div class="sub truncate">
                {nameOf(agents.value, rule.agent_id)} · {nameOf(servers.value, rule.server_id)} · {rule.tool ?? "any tool"} · {relative(rule.created_at)}
              </div>
            </div>
          ))}
        </div>
      )}
      </Screen>
    </div>
  );
}
