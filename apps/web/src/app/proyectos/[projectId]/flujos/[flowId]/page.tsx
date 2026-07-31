import { EditorClient } from "@/components/editor/EditorClient";

export default async function FlowEditorPage({
  params,
}: {
  params: Promise<{ projectId: string; flowId: string }>;
}) {
  const { projectId, flowId } = await params;
  return <EditorClient projectId={projectId} flowId={flowId} />;
}
