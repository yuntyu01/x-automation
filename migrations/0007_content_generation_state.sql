-- 재생성·이미지 추가 요청은 제안만 만들고 글 상태를 바꾸지 않는다.
-- 예전 코드가 남긴 '생성 중'·'실패' 표시를 지워, 그대로 게시할 수 있는 글이 '문제'·'초안'으로 묶여 있지 않게 한다
UPDATE contents SET generation_state = 'none', generation_error = NULL WHERE generation_state <> 'none';
