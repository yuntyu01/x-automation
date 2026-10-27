import type { PromptVariable } from '../../api/types';
import { Select, TextArea } from '../ui/Field';
import { TextField } from '../ui/TextField';

interface Props {
  variables: PromptVariable[];
  values: Record<string, string>;
  onChange: (v: Record<string, string>) => void;
  errors?: Record<string, string>; // 키: 변수 이름
}

// 프롬프트 변수 입력 폼 (입력해 보기·AI 생성에서 같이 쓴다). 비우면 기본값이 들어간다.
export function VariablesForm({ variables, values, onChange, errors = {} }: Props) {
  const set = (name: string, v: string) => onChange({ ...values, [name]: v });
  return (
    <>
      {variables.map((v) => {
        const label = v.label + (v.required ? ' *' : '');
        if (v.type === 'select') {
          return (
            <Select
              key={v.name}
              label={label}
              value={values[v.name] ?? v.default}
              onChange={(e) => set(v.name, e.target.value)}
              options={(v.options ?? []).map((o) => ({ value: o, label: o }))}
              error={errors[v.name]}
              hint={v.help}
            />
          );
        }
        if (v.type === 'textarea') {
          return <TextArea key={v.name} label={label} value={values[v.name] ?? ''} placeholder={v.default} onChange={(e) => set(v.name, e.target.value)} error={errors[v.name]} hint={v.help} />;
        }
        return (
          <TextField
            key={v.name}
            label={label}
            type={v.type === 'number' ? 'number' : 'text'}
            inputMode={v.type === 'number' ? 'numeric' : undefined}
            value={values[v.name] ?? ''}
            placeholder={v.default}
            onChange={(e) => set(v.name, e.target.value)}
            error={errors[v.name]}
            hint={v.help}
          />
        );
      })}
    </>
  );
}
