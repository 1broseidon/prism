import { useState } from "preact/hooks";
import { copyToClipboard } from "../util/clipboard";

interface Props {
  value: string;
  label: string;
}

export function CopyId({ value, label }: Props) {
  const [copied, setCopied] = useState(false);
  const onClick = (e: MouseEvent) => {
    e.stopPropagation();
    copyToClipboard(value, () => {
      setCopied(true);
      setTimeout(() => setCopied(false), 1200);
    });
  };
  return (
    <span
      class={copied ? "copy-id copied" : "copy-id"}
      title={value}
      onClick={onClick}
    >
      {copied ? "copied" : label}
    </span>
  );
}
