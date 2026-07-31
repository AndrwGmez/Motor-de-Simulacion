import { EditorClient } from "@/components/editor/EditorClient";

export default async function SharedFlowPage({ params }: { params: Promise<{ token: string }> }) {
  const { token } = await params;
  return <EditorClient flowId="pedidos" readOnly shareToken={token} />;
}
