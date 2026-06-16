import type { WorkstationInfo } from "./api";

export interface MachineSummary {
  id: string;
  number: number;
  label: string;
  workstation: WorkstationInfo;
}

export function buildMachineSummaries(
  workstations: WorkstationInfo[],
): MachineSummary[] {
  return [...workstations]
    .sort((left, right) => {
      const createdAtComparison = left.created_at.localeCompare(right.created_at);
      if (createdAtComparison !== 0) {
        return createdAtComparison;
      }
      return left.id.localeCompare(right.id);
    })
    .map((workstation, index) => ({
      id: workstation.id,
      number: index + 1,
      label: `Machine ${index + 1}`,
      workstation,
    }));
}
