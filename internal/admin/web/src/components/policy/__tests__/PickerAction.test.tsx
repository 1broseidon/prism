// PickerAction type-smoke "tests".
//
// `npm test` is `tsc --noEmit`, so this file pins:
//   1. PickerAction prop contract — verbs + backends both required.
//   2. VerbResolutionPreview prop contract — slug + enabledBackends.
//   3. Each of the three ActionSpec modes (verb / tool / backend_wildcard)
//      is assignable to the value prop.

import type { JSX } from "preact";
import type { ActionSpec, Verb } from "../../../api/policy";
import {
  PickerAction,
  VerbResolutionPreview,
  type BackendOption,
} from "../PickerAction";

const verbs: readonly Verb[] = [
  {
    slug: "write_files",
    label: "Write files",
    patterns: [
      { backend: "fs", tools: ["write_file", "append_file", "delete_file"] },
    ],
  },
  {
    slug: "read_files",
    label: "Read files",
    patterns: [{ backend: "fs", tools: ["read_file", "list_dir"] }],
  },
];

const backends: readonly BackendOption[] = [
  { id: "fs", label: "fs", tools: ["write_file", "read_file"] },
  { id: "github", label: "github", tools: ["create_issue", "create_pr"] },
];

// Each mode is constructable + assignable to value.
const verbValue: ActionSpec = { mode: "verb", verb_slug: "write_files" };
const toolValue: ActionSpec = {
  mode: "tool",
  backend: "fs",
  tool: "write_file",
};
const wildcardValue: ActionSpec = {
  mode: "backend_wildcard",
  backend: "fs",
};

// The picker must accept any of the three values without complaint.
const inVerb: JSX.Element = (
  <PickerAction
    value={verbValue}
    onChange={(next) => {
      void next;
    }}
    verbs={verbs}
    backends={backends}
  />
);
const inTool: JSX.Element = (
  <PickerAction
    value={toolValue}
    onChange={() => {
      /* no-op */
    }}
    verbs={verbs}
    backends={backends}
    disabled={false}
  />
);
const inWildcard: JSX.Element = (
  <PickerAction
    value={wildcardValue}
    onChange={() => {
      /* no-op */
    }}
    verbs={verbs}
    backends={backends}
  />
);

// The resolution preview is exported so the parent (or future tests) can
// render the chip strip standalone for a given verb.
const resolutionPreview: JSX.Element = (
  <VerbResolutionPreview slug="write_files" enabledBackends={["fs"]} />
);

// Zero-tool resolution case — captured here so a future runtime test can
// snapshot the warning chip. At compile time we just lock the props.
const zeroToolPreview: JSX.Element = (
  <VerbResolutionPreview slug="missing_verb" enabledBackends={[]} />
);

export const __pickerActionTypeChecks = {
  inVerb,
  inTool,
  inWildcard,
  resolutionPreview,
  zeroToolPreview,
};
