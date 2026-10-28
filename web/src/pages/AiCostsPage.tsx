import { useState } from 'react';
import { ExternalLink, Image, Sparkles } from 'lucide-react';
import { PageHeader } from '../components/layout/AppShell';
import styles from './AiCostsPage.module.css';

// Standard API rates, checked against provider documentation on 2026-10-29.
const textModels = [
  { provider: 'Gemini', name: '3.5 Flash-Lite', model: 'gemini-3.5-flash-lite', input: 0.30, output: 2.50, free: '하루 500회 · 분당 15회', current: true },
  { provider: 'Gemini', name: '3.1 Flash-Lite', model: 'gemini-3.1-flash-lite', input: 0.25, output: 1.50, free: '하루 500회 · 분당 15회' },
  { provider: 'OpenAI', name: 'GPT-5.4 nano', model: 'gpt-5.4-nano', input: 0.20, output: 1.25, free: '무료 API 등급 미지원' },
  { provider: 'OpenAI', name: 'GPT-5.4 mini', model: 'gpt-5.4-mini', input: 0.75, output: 4.50, free: '무료 API 등급 미지원' },
];
const imageModels = [
  { provider: 'Gemini', name: '3.1 Flash Lite Image', model: 'gemini-3.1-flash-lite-image', free: '무료 API 미지원', cost: 0.0336, detail: '1K · 이미지 출력', current: true },
  { provider: 'Gemini', name: '3.1 Flash Image', model: 'gemini-3.1-flash-image', free: '무료 API 미지원', cost: 0.067, detail: '1K · 이미지 출력' },
  { provider: 'OpenAI', name: 'GPT Image 2 · 낮음', model: 'gpt-image-2', free: '무료 API 미지원', cost: 0.006, detail: '1024×1024 · 낮은 품질' },
  { provider: 'OpenAI', name: 'GPT Image 2 · 보통', model: 'gpt-image-2', free: '무료 API 미지원', cost: 0.053, detail: '1024×1024 · 보통 품질' },
  { provider: 'OpenAI', name: 'GPT Image 2 · 높음', model: 'gpt-image-2', free: '무료 API 미지원', cost: 0.211, detail: '1024×1024 · 높은 품질' },
];
const dollars = (n: number) => n > 0 && n < 0.000001 ? '<$0.000001' : `$${n.toFixed(n < 0.01 ? 6 : 4).replace(/0+$/, '').replace(/\.$/, '')}`;

export function AiCostsPage() {
  const [quantity, setQuantity] = useState(20);
  const [input, setInput] = useState(1000);
  const [output, setOutput] = useState(500);
  const number = (value: string, max: number) => Math.max(0, Math.min(max, Number(value) || 0));
  return (
    <>
      <PageHeader title="AI 비용" subtitle="Gemini와 GPT API 요금을 한눈에 비교해요" />
      <div className={styles.layout}>
        <div className={styles.notice}>
          <strong>현재 연동: Gemini</strong>
          <p>이 앱에서 시험한 무료 프로젝트는 글 생성이 가능해요. 이미지 생성은 무료 한도가 없어 유료 결제 설정이 필요해요. GPT는 아래에 비교용으로 표시하며 현재 연결되어 있지 않아요.</p>
        </div>
        <section className={styles.panel}>
          <div className={styles.heading}><Sparkles size={20} aria-hidden /><div><h2>글 생성</h2><p>입력과 출력 토큰에 따라 실제 비용이 달라져요</p></div></div>
          <div className={styles.controls}>
            <label>글 개수<input type="number" min="1" max="1000" value={quantity} onChange={(e) => setQuantity(number(e.target.value, 1000))} /></label>
            <label>글당 입력 토큰<input type="number" min="0" max="1000000" value={input} onChange={(e) => setInput(number(e.target.value, 1000000))} /></label>
            <label>글당 출력 토큰<input type="number" min="0" max="1000000" value={output} onChange={(e) => setOutput(number(e.target.value, 1000000))} /></label>
          </div>
          <p className={styles.note}>예상 예시예요. 입력에는 프롬프트가, 출력에는 답변과 추론 토큰이 포함될 수 있어요. 무료 등급의 한도 내 비용은 $0이며, 표의 금액은 유료 표준 API 요금입니다.</p>
          <div className={styles.tableWrap}><table><thead><tr><th scope="col">모델</th><th scope="col">무료 사용</th><th scope="col">100만 토큰당 입력 / 출력</th><th scope="col">글 1개 예시</th><th scope="col">{quantity}개 예시</th></tr></thead>
            <tbody>{textModels.map((m) => { const price = (m.input * input + m.output * output) / 1_000_000; return <tr key={m.model}><th scope="row"><span className={styles.provider}>{m.provider}</span><strong>{m.name}</strong>{m.current && <span className={styles.badge}>사용 중인 글 모델</span>}<small>{m.model}</small></th><td>{m.free}</td><td>${m.input} / ${m.output}</td><td>{dollars(price)}</td><td><strong>{dollars(price * quantity)}</strong></td></tr>; })}</tbody></table></div>
        </section>
        <section className={styles.panel}>
          <div className={styles.heading}><Image size={20} aria-hidden /><div><h2>이미지 생성</h2><p>정사각형 1장 기준 · 입력 토큰 비용 별도</p></div></div>
          <div className={styles.tableWrap}><table><thead><tr><th scope="col">모델</th><th scope="col">무료 사용</th><th scope="col">기준</th><th scope="col">1장 출력</th><th scope="col">{quantity}장 출력</th></tr></thead>
            <tbody>{imageModels.map((m) => <tr key={m.name}><th scope="row"><span className={styles.provider}>{m.provider}</span><strong>{m.name}</strong>{m.current && <span className={styles.badge}>설정된 이미지 모델</span>}<small>{m.model}</small></th><td>{m.free}</td><td>{m.detail}</td><td>{dollars(m.cost)}</td><td><strong>{dollars(m.cost * quantity)}</strong></td></tr>)}</tbody></table></div>
          <p className={styles.note}>이미지 가격은 출력분만 계산했어요. 이미지 설명과 입력 이미지, 생성된 텍스트·추론, 재시도 등에 따라 추가 비용이 생길 수 있어요. 모델과 품질마다 결과가 다릅니다.</p>
        </section>
        <section className={styles.panel}>
          <h2>무료 한도와 확인 방법</h2>
          <ul className={styles.facts}>
            <li><strong>Gemini 글:</strong> 2026년 10월 29일 x-automation-test 무료 프로젝트에서 각 글 모델당 하루 500회·분당 15회·분당 입력 25만 토큰 한도를 확인했어요. 요청 수는 글 개수와 다를 수 있어요. 한도는 달라질 수 있으며 이 화면에서 남은 양을 실시간 조회하지 않아요.</li>
            <li><strong>이미지:</strong> 위 Gemini 이미지 모델과 GPT Image 2는 API 무료 등급을 제공하지 않아요. Google AI Studio의 웹 체험과 API 사용 한도는 다를 수 있어요.</li>
            <li><strong>GPT:</strong> 표의 GPT API 모델은 무료 등급을 지원하지 않아요. ChatGPT 구독과 API 결제는 별도이며, 보유한 프로모션 크레딧은 계정에 따라 달라요.</li>
            <li><strong>실제 결제:</strong> 2026년 10월 29일 확인한 미국 달러 표준 요금입니다. 세금, 환율, 사용량, 요금 변경은 반영되지 않아요. 제공사 청구 내역이 기준입니다.</li>
          </ul>
          <div className={styles.links}>
            <a href="https://ai.google.dev/gemini-api/docs/pricing" target="_blank" rel="noreferrer">Google 공식 요금 <ExternalLink size={15} /></a>
            <a href="https://aistudio.google.com/rate-limit" target="_blank" rel="noreferrer">Gemini 프로젝트 한도 확인 <ExternalLink size={15} /></a>
            <a href="https://developers.openai.com/api/docs/pricing" target="_blank" rel="noreferrer">OpenAI 공식 요금 <ExternalLink size={15} /></a>
            <a href="https://developers.openai.com/api/docs/guides/image-generation" target="_blank" rel="noreferrer">OpenAI 이미지 요금 <ExternalLink size={15} /></a>
            <a href="https://platform.openai.com/usage" target="_blank" rel="noreferrer">OpenAI 사용량 확인 <ExternalLink size={15} /></a>
          </div>
        </section>
      </div>
    </>
  );
}
