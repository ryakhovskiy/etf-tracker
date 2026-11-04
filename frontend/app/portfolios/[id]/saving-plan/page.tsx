import { redirect } from "next/navigation";
export default async function SavingPlanPage({ params }: { params: Promise<{ id: string }> }) {
  const { id } = await params;
  redirect(`/portfolios/${encodeURIComponent(id)}/transactions`);
}
