# 외부 연동 확인 기록

확인일: 2026-10-27. 이 개발 환경에서는 docs.x.com·developers.cloudflare.com·developers.openai.com이 직접 열리지 않아,
각 문서 사이트의 **공식 소스 저장소**(github.com/xdevplatform/docs `f6db7d0`, github.com/cloudflare/cloudflare-docs `1ad2be0`)와
OpenAI 공식 Go SDK(openai/openai-go v3.88.0)의 생성 타입으로 확인했다. 표시:

- **확인** — 공식 소스·명세에서 읽음
- **부분 확인** — 공식 도메인이지만 검색 요약으로만 봄
- **미확인** — 공식 근거 없음. 실제 계정으로 확인 필요

요금·제한은 바뀔 수 있다. 실제 운영 전 각 콘솔에서 다시 확인하고 설정 화면의 요율표(확인일·출처)를 갱신한다.

## X API v2

| 항목 | 내용 | 상태 |
|---|---|---|
| 게시 | `POST https://api.x.com/2/tweets` 본문 `{"text", "media":{"media_ids":[최대 4]}, "reply":{"in_reply_to_tweet_id"}}`. 모르는 필드 거부. 성공 201 `{"data":{"id","text"}}` | 확인 |
| 오류 형식 | Problem JSON `{type,title,detail,status}`. 429는 `x-rate-limit-reset` 헤더 | 확인 |
| 멱등성 키 | **없음**. 앱이 전송 의도 기록·성공 ID 즉시 저장·불명확 결과 보류로 중복을 막는다 | 확인 |
| 인증 | OAuth 2.0 Authorization Code + PKCE. 인가 `https://x.com/i/oauth2/authorize`, 토큰 `POST https://api.x.com/2/oauth2/token`, 해제 `POST https://api.x.com/2/oauth2/revoke`. 서버 앱은 Web App(기밀 클라이언트)으로 등록하고 토큰 요청에 Basic 인증 | 확인 |
| 권한 | `tweet.read tweet.write users.read media.write offline.access` (미디어 업로드는 `media.write` 필수) | 확인 |
| 토큰 수명 | 접근 토큰 2시간. 갱신 토큰 약 6개월·**1회용**(갱신마다 새 토큰). 갱신은 계정당 하나씩 직렬화하고 새 토큰을 즉시 저장 | 확인 |
| 인가 코드 | 발급 후 30초 안에 교환해야 함 | 확인 |
| 미디어 업로드 | 간단 업로드 `POST /2/media/upload` (multipart `media` + `media_category=tweet_image`), 분할 업로드 initialize/append/finalize. OAuth 2.0 사용자 토큰 허용. 이미지 JPG·PNG·GIF·WEBP, **5MB 이하**, 게시당 4장 | 확인 |
| 자기 답글 제한 | 2026-02-23 변경: 셀프서브 등급의 API 답글은 원글 작성자가 답글 계정을 멘션·인용한 경우만 허용. **자기 글에 답글(타래)이 예외인지 공식 문서에 없음** | 미확인 |
| 가중 글자 수 | 280 한도, URL 23, NFC 정규화, U+0000~U+10FF 등 일부 범위만 1·나머지(한글·CJK·이모지) 2. twitter-text config v3 | 확인 |
| 장문 게시 | API에서 25,000자 장문 생성 가능(2024-08 변경 이력). 어떤 구독 등급에 허용되는지는 문서에 없음. `GET /2/users/me?user.fields=subscription_type`로 구독 종류 확인 가능 | 부분 확인 |
| 요금 | 종량제(크레딧). 게시 $0.015, **URL 포함 게시 $0.200**, 소환된 답글 $0.010 (2026-04-20 적용). 미디어 업로드 요금은 표에 없음 | 확인(미디어는 미확인) |
| 호출 제한 | `POST /2/tweets` 사용자당 100회/15분(다른 문서는 200회/15분·300회/3시간), 간단 업로드 500회/15분, `/2/users/me` 75회/15분 | 확인(문서 간 차이 있음) |
| AI 표시 | 게시 요청에 `made_with_ai: true` 필드(AI 생성 미디어 공개)가 명세에 있음 | 확인(명세만) |

### 앱에 반영한 결정

- 답글이 403으로 거부되면 묶음을 "일부 완료"로 두고 본문을 다시 올리지 않는다. 원인을 알림에 그대로 보여 주고, 재시도·수동 연결로 해결한다
- 기본은 표준 280 모드. 장문은 설정에서 명시적으로 켜야 하고, `subscription_type`이 None이면 켤 수 없게 안내한다
- URL이 포함된 게시는 비용이 약 13배라 편집·미리보기에서 경고한다
- AI 생성 이미지를 첨부할 때 `made_with_ai`를 보낼지는 설정으로 둔다(기본 켬). AI 이미지가 실제로 붙은 부분에만 보낸다
- 오류 분류(`xapi/http.go`): 연결 자체 실패(DNS·연결 거부)는 보내지 않은 것으로 보고 재시도, 요청을 보낸 뒤 끊기거나 시간 초과·게시 5xx는 결과 불명확, 429는 `x-rate-limit-reset`까지 대기, 401은 토큰 갱신 후 한 번 재시도, 403·400은 실패(사유 그대로 표시)
- 미디어 ID는 24시간 뒤 만료(`expires_after_secs`)되므로 20시간 넘은 ID는 다시 올린다
- 연결 시 `GET /2/users/me?user.fields=subscription_type`의 등급을 저장한다. None이면 장문 게시를 막고, 그 밖의 등급은 "가능할 수 있음"으로만 표시한다(허용 등급 문서 없음)
- 실제 X 공개 게시는 개발·검증 기본값으로 쓰지 않았다. 게시 경로는 가짜 X 서버(`internal/integration/xapi/xfake`)로 검증했다. 운영 첫 게시 전 확인할 것: 자기 답글이 2026-02 답글 제한의 예외인지(위 미확인 항목)

## OpenAI (텍스트·이미지 공급자)

| 항목 | 내용 | 상태 |
|---|---|---|
| 텍스트 | `POST https://api.openai.com/v1/responses`, `text.format = {type:"json_schema", name, strict:true, schema}`. 응답 `output[].content[]`의 `output_text`를 이어 붙이고 `refusal`·`status:"incomplete"`를 먼저 확인 | 확인 |
| 텍스트 모델 | SDK에 `gpt-6.1-terra`, `gpt-6-luna` 등 존재. 권장 모델은 확인 못함 → `AI_TEXT_MODEL`로 지정 | 부분 확인 |
| 이미지 | `POST /v1/images/generations`. `gpt-image-1`은 2026-10-23 종료. `gpt-image-2.5-sunburst`·`gpt-image-2.5-flare` 등. `size`, `quality`, `n`, `output_format(png/jpeg/webp)`, `output_compression`. 항상 `b64_json` | 확인 |
| 요금 | `gpt-6.1-terra` 입력 $1.00·출력 $5.00 /1M 토큰, `gpt-6-luna` $0.10·$0.50. 이미지 2.5는 이미지 출력 토큰 $30/1M, 장당 가격은 확인 못함 | 부분 확인 |

앱은 요율을 코드에 고정하지 않는다. 설정의 요율표에 확인일·출처를 함께 입력하고, 비어 있으면 "단가 미확인"으로 표시한다.

## Cloudflare R2

| 항목 | 내용 | 상태 |
|---|---|---|
| 엔드포인트 | `https://<ACCOUNT_ID>.r2.cloudflarestorage.com`, 리전 `auto`. PutObject·GetObject·HeadObject·DeleteObject·ListObjectsV2 지원 | 확인 |
| SDK 주의 | aws-sdk-go-v2 최근 기본 체크섬(CRC32) 때문에 R2에서 오류 사례 → `RequestChecksumCalculation`·`ResponseChecksumValidation`을 `WhenRequired`로 설정. DeleteObjects 대신 단건 DeleteObject 사용 | 부분 확인(2차 자료) |
| 공개 조회 | 커스텀 도메인 권장. `r2.dev`는 속도 제한이 있어 개발용 | 확인 |
| 요금 | 저장 $0.015/GB-월, Class A $4.50/백만, Class B $0.36/백만, 송신 무료. 무료 구간 10GB·A 100만·B 1,000만 | 확인 |
| 수명주기 | 업로드 후 경과일 기준 규칙. 앱의 "마지막 게시 후 14일" 규칙을 대신하지 않는다 | 확인 |

## KoDeploy (github.com/kodeploy/kodeploy `38d1275`)

| 항목 | 내용 |
|---|---|
| 배포 방식 | 웹 UI에서 배포. CLI·앱별 설정 파일 없음. **main 푸시만으로 자동 배포되지 않음**(푸시 후 UI에서 재배포) |
| 빌드 | 가장 얕은 `Dockerfile` 사용, 그 폴더가 빌드 컨텍스트. Dockerfile 빌드에는 build arg 없음. 클러스터는 **ARM64**(Ampere) |
| 실행 | `PORT` 주입(Go 기본 8080), `0.0.0.0:$PORT` 바인딩. UID 1000 non-root, 메모리 256Mi, CPU 300m, replicas 1·Recreate(배포 때 잠깐 중단) |
| 헬스체크 | TCP 프로브만 사용(시작 60초 이내). `/healthz`·`/readyz`는 앱 자체 진단용 |
| DB | 관리형 PostgreSQL 16. `DB_HOST·DB_PORT·DB_NAME·DB_USER·DB_PASSWORD` 주입. 함께 주입되는 `DATABASE_URL`은 SQLAlchemy 형식이라 앱이 `postgresql+psycopg2://`를 정규화하거나 `DB_*`로 연결 문자열을 만든다 |
| 오브젝트 스토리지 | 선택 시 `S3_*`·`AWS_*` 주입. 공개 주소가 r2.dev이고 **키가 재배포마다 바뀜**. 앱은 R2_*가 없을 때 S3_*를 대신 읽는다. 운영은 전용 버킷·커스텀 도메인 권장 |
| 환경변수 | UI의 환경변수 화면 → K8s Secret. 키 50개·값 4096자 제한. 저장 시 재빌드 없이 재시작 |
| OAuth 콜백 | `https://{앱이름}-api.kodeploy.com/api/x/oauth/callback` (정적 슬롯 유무와 관계없이 유지되는 주소) |
| 미확인 | `pipeline v2` 사용자의 배포 경로(외부 Helm 차트)는 저장소에 없어 프로브·환경 주입이 다를 수 있음 |

## twitter-text

- config v3: `maxWeightedTweetLength 280, scale 100, defaultWeight 200, transformedURLLength 23, emojiParsingEnabled true`,
  1로 세는 범위 U+0000–U+10FF, U+2000–U+200D, U+2010–U+201F, U+2032–U+2037 (확인)
- npm `twitter-text` 최신 3.1.0(2020). Go 공식 포팅 없음 → 서버는 같은 라이브러리에서 정규식을 생성해(`web/scripts/gen-thread-data.mjs` → `internal/thread/twittertext_gen.go`) 같은 알고리즘으로 실행한다
- 이모지 인식은 라이브러리가 묶어 쓰는 `twemoji-parser` 11.0.2(이모지 11 기준). 그 뒤 나온 이모지 시퀀스(예: 🧑‍🤝‍🧑, ❤️‍🔥)는 코드 포인트마다 세어 X 실제 계산보다 길게 나올 수 있다 → 한도를 넘는 쪽이 아니라 덜 채우는 쪽의 오차라 그대로 둔다 (확인: 라이브러리 소스)
- 공식 적합성 사례(validate.yml·extract.yml)를 `testdata/twitter-text`에 두고 Go가 전부 통과한다. 프론트와 서버의 일치는 라이브러리로 계산한 `testdata/thread/golden.json`(문장 180개, 이모지 1,247개 × 변형 5)으로 양쪽 테스트에서 대조한다
- 장문 게시(25,000자): 계정 등급과 API 권한에 따라 다르고 이 저장소에서 확인할 수 없다 → 기본 꺼짐, 설정에서 사용자가 확인 후 켠다
