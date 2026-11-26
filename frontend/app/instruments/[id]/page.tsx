import { InstrumentView } from "../../../components/instrument-view";

export default async function InstrumentPage({ params }: { params: Promise<{ id: string }> }) {
  const { id } = await params;
  return <InstrumentView id={id} />;
}
