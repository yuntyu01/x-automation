import { useState } from 'react';
import { Calculator, ExternalLink, Image, Sparkles } from 'lucide-react';
import { PageHeader } from '../components/layout/AppShell';
import { Segmented } from '../components/ui/Segmented';
import { TextField } from '../components/ui/TextField';
import { ApiKeyGuide } from './ApiKeyGuide';
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
const currentText = textModels.find((m) => m.current)!;
const currentImage = imageModels.find((m) => m.current)!;

type Currency = 'usd' | 'krw';
const DEFAULT_RATE = 1400;
const CURRENCY_KEY = 'xa-ai-cost-currency';
const RATE_KEY = 'xa-ai-cost-rate';

// 통화·환율은 이 브라우저에만 기억한다 (저장소가 막혀 있어도 화면은 동작)
function remembered(key: string): string | null {
  try {
    return localStorage.getItem(key);
  } catch {
    return null;
  }
}
function remember(key: string, value: string) {
  try {
    localStorage.setItem(key, value);
  } catch {
    // 저장이 막혀도 이번 화면에서는 바꾼다
  }
}

const dollars = (n: number) => n > 0 && n < 0.000001 ? '<$0.000001' : `$${n.toFixed(n < 0.01 ? 6 : 4).replace(/0+$/, '').replace(/\.$/, '')}`;
const won = (n: number) => {
  if (n === 0) return '0원';
  if (n < 0.01) return '0.01원 미만';
  const digits = n < 1 ? 2 : n < 100 ? 1 : 0;
  return `${n.toLocaleString('ko-KR', { maximumFractionDigits: digits })}원`;
};

export function AiCostsPage() {
  const [quantity, setQuantity] = useState(20);
  const [images, setImages] = useState(20);
  const [input, setInput] = useState(1000);
  const [output, setOutput] = useState(500);
  const [currency, setCurrency] = useState<Currency>(() => (remembered(CURRENCY_KEY) === 'krw' ? 'krw' : 'usd'));
  const [rateText, setRateText] = useState(() => remembered(RATE_KEY) ?? String(DEFAULT_RATE));
  const parsedRate = Number(rateText.replace(/,/g, ''));
  const rateValid = Number.isFinite(parsedRate) && parsedRate > 0;
  const rate = rateValid ? parsedRate : DEFAULT_RATE;
  const krw = currency === 'krw';
  const money = (usd: number) => (krw ? won(usd * rate) : dollars(usd));
  const number = (value: string, max: number) => Math.max(0, Math.min(max, Number(value) || 0));

  const textEach = (currentText.input * input + currentText.output * output) / 1_000_000;
  const textTotal = textEach * quantity;
  const imageTotal = currentImage.cost * images;

  return (
    <>
      <PageHeader title="AI 비용" subtitle="Gemini와 GPT API 요금을 한눈에 비교해요" />
      <div className={styles.layout}>
        <section className={styles.panel} aria-labelledby="quick-calc">
          <div className={styles.calcHead}>
            <div className={styles.heading}>
              <Calculator size={20} aria-hidden />
              <div>
                <h2 id="quick-calc">간단 계산</h2>
                <p>글과 이미지를 이만큼 만들면 대략 이 정도예요</p>
              </div>
            </div>
            <div className={styles.currency}>
              <Segmented
                variant="segment"
                label="금액 표시"
                value={currency}
                onChange={(v) => {
                  setCurrency(v);
                  remember(CURRENCY_KEY, v);
                }}
                options={[
                  { value: 'usd', label: '달러' },
                  { value: 'krw', label: '원화' },
                ]}
              />
            </div>
          </div>
          <div className={styles.calcInputs}>
            <TextField label="글" type="number" inputMode="numeric" min={0} max={1000} value={String(quantity)} onChange={(e) => setQuantity(number(e.target.value, 1000))} suffix="개" />
            <TextField label="이미지" type="number" inputMode="numeric" min={0} max={1000} value={String(images)} onChange={(e) => setImages(number(e.target.value, 1000))} suffix="장" />
            {krw && (
              <TextField
                className={styles.rate}
                label="환율 (1달러당)"
                inputMode="decimal"
                value={rateText}
                onChange={(e) => {
                  setRateText(e.target.value);
                  remember(RATE_KEY, e.target.value);
                }}
                suffix="원"
                error={rateValid ? undefined : `0보다 큰 숫자를 넣어 주세요. 지금은 ${DEFAULT_RATE.toLocaleString('ko-KR')}원으로 계산해요`}
                hint={`기본 ${DEFAULT_RATE.toLocaleString('ko-KR')}원. 실제 청구는 카드사 환율·수수료에 따라 달라요`}
              />
            )}
          </div>
          <div className={styles.result} aria-live="polite">
            <p className={styles.resultLabel}>예상 비용 (유료 요금 기준)</p>
            <p className={styles.total}>약 {money(textTotal + imageTotal)}</p>
            <dl className={styles.breakdown}>
              <div>
                <dt>
                  글 {quantity.toLocaleString('ko-KR')}개 <small>{currentText.provider} {currentText.name}</small>
                </dt>
                <dd>{money(textTotal)}</dd>
              </div>
              <div>
                <dt>
                  이미지 {images.toLocaleString('ko-KR')}장 <small>{currentImage.provider} {currentImage.name}</small>
                </dt>
                <dd>{money(imageTotal)}</dd>
              </div>
            </dl>
          </div>
          <p className={styles.note}>
            글 1개는 입력 {input.toLocaleString('ko-KR')} · 출력 {output.toLocaleString('ko-KR')} 토큰으로 셌어요(아래 글 생성에서 바꿀 수 있어요). Gemini 무료 한도 안의 글 생성은 {money(0)}이에요.
          </p>
        </section>

        <div className={styles.notice}>
          <strong>현재 연동: Gemini</strong>
          <p>이 앱에서 시험한 무료 프로젝트는 글 생성이 가능해요. 이미지 생성은 무료 한도가 없어 유료 결제 설정이 필요해요. GPT는 아래에 비교용으로 표시하며 현재 연결되어 있지 않아요.</p>
        </div>
        <section className={styles.panel}>
          <div className={styles.heading}><Sparkles size={20} aria-hidden /><div><h2>글 생성</h2><p>입력과 출력 토큰에 따라 실제 비용이 달라져요</p></div></div>
          <div className={styles.controls}>
            <label>글당 입력 토큰<input type="number" min="0" max="1000000" value={input} onChange={(e) => setInput(number(e.target.value, 1000000))} /></label>
            <label>글당 출력 토큰<input type="number" min="0" max="1000000" value={output} onChange={(e) => setOutput(number(e.target.value, 1000000))} /></label>
          </div>
          <p className={styles.note}>예상 예시예요. 입력에는 프롬프트가, 출력에는 답변과 추론 토큰이 포함될 수 있어요. 무료 등급의 한도 내 비용은 {money(0)}이며, 표의 금액은 유료 표준 API 요금입니다. 글 개수는 위 간단 계산에서 바꿔요.</p>
          <div className={styles.tableWrap}><table><thead><tr><th scope="col">모델</th><th scope="col">무료 사용</th><th scope="col">100만 토큰당 입력 / 출력</th><th scope="col">글 1개 예시</th><th scope="col">{quantity}개 예시</th></tr></thead>
            <tbody>{textModels.map((m) => { const price = (m.input * input + m.output * output) / 1_000_000; return <tr key={m.model}><th scope="row"><span className={styles.provider}>{m.provider}</span><strong>{m.name}</strong>{m.current && <span className={styles.badge}>사용 중인 글 모델</span>}<small>{m.model}</small></th><td>{m.free}</td><td>{money(m.input)} / {money(m.output)}</td><td>{money(price)}</td><td><strong>{money(price * quantity)}</strong></td></tr>; })}</tbody></table></div>
        </section>
        <section className={styles.panel}>
          <div className={styles.heading}><Image size={20} aria-hidden /><div><h2>이미지 생성</h2><p>정사각형 1장 기준 · 입력 토큰 비용 별도</p></div></div>
          <div className={styles.tableWrap}><table><thead><tr><th scope="col">모델</th><th scope="col">무료 사용</th><th scope="col">기준</th><th scope="col">1장 출력</th><th scope="col">{images}장 출력</th></tr></thead>
            <tbody>{imageModels.map((m) => <tr key={m.name}><th scope="row"><span className={styles.provider}>{m.provider}</span><strong>{m.name}</strong>{m.current && <span className={styles.badge}>설정된 이미지 모델</span>}<small>{m.model}</small></th><td>{m.free}</td><td>{m.detail}</td><td>{money(m.cost)}</td><td><strong>{money(m.cost * images)}</strong></td></tr>)}</tbody></table></div>
          <p className={styles.note}>이미지 가격은 출력분만 계산했어요. 이미지 설명과 입력 이미지, 생성된 텍스트·추론, 재시도 등에 따라 추가 비용이 생길 수 있어요. 모델과 품질마다 결과가 다릅니다.</p>
        </section>
        <section className={styles.panel}>
          <h2>무료 한도와 확인 방법</h2>
          <ul className={styles.facts}>
            <li><strong>Gemini 글:</strong> 2026년 10월 29일 x-automation-test 무료 프로젝트에서 각 글 모델당 하루 500회·분당 15회·분당 입력 25만 토큰 한도를 확인했어요. 요청 수는 글 개수와 다를 수 있어요. 한도는 달라질 수 있으며 이 화면에서 남은 양을 실시간 조회하지 않아요.</li>
            <li><strong>이미지:</strong> 위 Gemini 이미지 모델과 GPT Image 2는 API 무료 등급을 제공하지 않아요. Google AI Studio의 웹 체험과 API 사용 한도는 다를 수 있어요.</li>
            <li><strong>GPT:</strong> 표의 GPT API 모델은 무료 등급을 지원하지 않아요. ChatGPT 구독과 API 결제는 별도이며, 보유한 프로모션 크레딧은 계정에 따라 달라요.</li>
            <li><strong>실제 결제:</strong> 2026년 10월 29일 확인한 미국 달러 표준 요금입니다. 원화는 입력한 환율로 바꾼 값이에요. 세금, 카드사 환율·수수료, 사용량, 요금 변경은 반영되지 않아요. 제공사 청구 내역이 기준입니다.</li>
          </ul>
          <div className={styles.links}>
            <a href="https://ai.google.dev/gemini-api/docs/pricing" target="_blank" rel="noreferrer">Google 공식 요금 <ExternalLink size={15} /></a>
            <a href="https://aistudio.google.com/rate-limit" target="_blank" rel="noreferrer">Gemini 프로젝트 한도 확인 <ExternalLink size={15} /></a>
            <a href="https://developers.openai.com/api/docs/pricing" target="_blank" rel="noreferrer">OpenAI 공식 요금 <ExternalLink size={15} /></a>
            <a href="https://developers.openai.com/api/docs/guides/image-generation" target="_blank" rel="noreferrer">OpenAI 이미지 요금 <ExternalLink size={15} /></a>
            <a href="https://platform.openai.com/usage" target="_blank" rel="noreferrer">OpenAI 사용량 확인 <ExternalLink size={15} /></a>
          </div>
        </section>
        <ApiKeyGuide />
      </div>
    </>
  );
}
