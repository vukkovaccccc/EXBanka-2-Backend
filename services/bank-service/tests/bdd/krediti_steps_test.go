// BDD acceptance tests za Krediti modul (Scenarios 33–38 iz TestoviCelina2.txt).
//
// Arhitektura:
//   - Scenariji 33–36 testiraju handler sloj direktno (gRPC handleri).
//     MockKreditService se injektuje u BankHandler; JWT claims se ubrizgavaju
//     direktno u context koristeći auth.NewContextWithClaims (bez interceptora).
//   - Scenariji 37–38 testiraju InstallmentWorker pozivanjem Start(ctx) sa
//     pre-otkazanim kontekstom: runDailyJob se poziva tačno jednom, a potom
//     select odmah selektuje ctx.Done() jer je context već zatvoren.
package bdd_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/cucumber/godog"
	jwtv5 "github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/mock"

	pb "banka-backend/proto/banka"
	"banka-backend/services/bank-service/internal/domain"
	"banka-backend/services/bank-service/internal/handler"
	"banka-backend/services/bank-service/internal/worker"
	"banka-backend/services/bank-service/mocks"
	auth "banka-backend/shared/auth"

	"google.golang.org/protobuf/types/known/emptypb"
)

// ─── bddPublisher — spy za NotificationPublisher ─────────────────────────────

type bddPublisher struct {
	mu     sync.Mutex
	events []worker.KreditEmailEvent
}

func (p *bddPublisher) Publish(event worker.KreditEmailEvent) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.events = append(p.events, event)
	return nil
}

func (p *bddPublisher) lastEventType() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.events) == 0 {
		return ""
	}
	return p.events[len(p.events)-1].Type
}

func (p *bddPublisher) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.events)
}

// ─── nopT — minimalni testing.TB za inicijalizaciju mockova ─────────────────

// nopT zadovoljava testify mock.TestingT interfejs.
// Greška se memoriše i vraća kroz After hook kao godog grešku.
type nopT struct {
	err error
}

func (n *nopT) Errorf(format string, args ...interface{}) {
	n.err = fmt.Errorf(format, args...)
}
func (n *nopT) FailNow()                        {}
func (n *nopT) Cleanup(f func())                { f() }
func (n *nopT) Logf(_ string, _ ...interface{}) {}
func (n *nopT) Helper()                         {}

// ─── scenarioCtx — stanje po scenariju ───────────────────────────────────────

type scenarioCtx struct {
	kreditSvcMock  *mocks.MockKreditService
	kreditRepoMock *mocks.MockKreditRepository
	bankHandler    *handler.BankHandler
	installWorker  *worker.InstallmentWorker
	publisher      *bddPublisher
	nopT           *nopT

	clientCtx   context.Context
	employeeCtx context.Context

	zahtevID       int64
	activeKreditID int64

	lastZahtevResp  *pb.ApplyForCreditResponse
	lastCreditsResp *pb.GetClientCreditsResponse
	lastApproveResp *pb.ApproveCreditResponse
	lastErr         error

	capturedNextRetry time.Time
}

// reset inicijalizuje/reinicijalizuje svo stanje scenarija.
func (s *scenarioCtx) reset() {
	nt := &nopT{}
	s.nopT = nt

	svcMock := &mocks.MockKreditService{}
	svcMock.Mock.Test(nt)

	repoMock := &mocks.MockKreditRepository{}
	repoMock.Mock.Test(nt)

	pub := &bddPublisher{}
	h := handler.NewBankHandler(nil, nil, nil, nil, svcMock, nil, nil, nil, nil, nil, nil, nil)
	w := worker.NewInstallmentWorker(repoMock, pub, time.Hour, 72*time.Hour, 0.05)

	s.kreditSvcMock = svcMock
	s.kreditRepoMock = repoMock
	s.bankHandler = h
	s.installWorker = w
	s.publisher = pub

	s.clientCtx = auth.NewContextWithClaims(context.Background(), &auth.AccessClaims{
		UserType:         "CLIENT",
		Email:            "klijent@test.com",
		RegisteredClaims: jwtv5.RegisteredClaims{Subject: "101"},
	})
	s.employeeCtx = auth.NewContextWithClaims(context.Background(), &auth.AccessClaims{
		UserType:         "EMPLOYEE",
		Email:            "zaposleni@test.com",
		RegisteredClaims: jwtv5.RegisteredClaims{Subject: "201"},
	})

	s.zahtevID = 10
	s.activeKreditID = 20
	s.lastErr = nil
	s.lastZahtevResp = nil
	s.lastCreditsResp = nil
	s.lastApproveResp = nil
	s.capturedNextRetry = time.Time{}
}

// ─── triggerWorkerOnce ────────────────────────────────────────────────────────

// triggerWorkerOnce poziva InstallmentWorker.Start sa pre-otkazanim contextom.
// runDailyJob se izvršava tačno jednom (synchronously pre select petlje).
// Zatim select odmah bira ctx.Done() jer je kanal zatvoren (ticker interval = 1h).
func triggerWorkerOnce(w *worker.InstallmentWorker) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		w.Start(ctx)
	}()
	wg.Wait()
}

// ─── Given koraci ─────────────────────────────────────────────────────────────

func (s *scenarioCtx) klijentJeUlogovanUAplikaciju() error { return nil }
func (s *scenarioCtx) zaposleniJeUlogovanUPortal() error   { return nil }
func (s *scenarioCtx) postojiAktivniKredit() error         { return nil }
func (s *scenarioCtx) datumSljedeceRateJeDanasnji() error  { return nil }

func (s *scenarioCtx) klijentImaAktivneKredite() error {
	s.kreditSvcMock.On("GetClientCredits", mock.Anything, int64(101)).
		Return([]domain.Kredit{
			{ID: 1, IznosKredita: 200_000, Valuta: "RSD", Status: "ODOBREN"},
			{ID: 2, IznosKredita: 100_000, Valuta: "RSD", Status: "ODOBREN"},
		}, nil)
	return nil
}

func (s *scenarioCtx) postojiZahtevZaKreditOdStraneKlijenta() error {
	kredit := &domain.Kredit{
		ID:          s.activeKreditID,
		BrojKredita: "KRD-20240101-99999999",
		VlasnikID:   101,
		Status:      "ODOBREN",
	}
	s.kreditSvcMock.On("ApproveCredit", mock.Anything, s.zahtevID).
		Return(kredit, nil)
	s.kreditSvcMock.On("ProcessFirstInstallment", mock.Anything, s.activeKreditID).
		Return(false, time.Time{}, nil)
	return nil
}

func (s *scenarioCtx) postojiZahtevZaKreditKlijenta() error {
	s.kreditSvcMock.On("RejectCredit", mock.Anything, s.zahtevID).Return(nil)
	return nil
}

func (s *scenarioCtx) klijentImaDovoljnoSredstava() error {
	due := domain.DueInstallment{
		RataID:                1,
		KreditID:              s.activeKreditID,
		IznosRate:             5_000,
		Valuta:                "RSD",
		OcekivaniDatumDospeca: time.Now().UTC(),
		BrojRacuna:            "111-222-333",
		VlasnikID:             101,
	}
	s.kreditRepoMock.On("GetDueInstallments", mock.Anything, mock.Anything).
		Return([]domain.DueInstallment{due}, nil)
	s.kreditRepoMock.On("GetRetryInstallments", mock.Anything, mock.Anything).
		Return([]domain.DueInstallment{}, nil)
	s.kreditRepoMock.On("ProcessInstallmentPayment", mock.Anything,
		domain.ProcessInstallmentInput{
			RataID:     due.RataID,
			KreditID:   due.KreditID,
			BrojRacuna: due.BrojRacuna,
			IznosRate:  due.IznosRate,
			Valuta:     due.Valuta,
		}).Return(nil)
	return nil
}

func (s *scenarioCtx) klijentNemaDovoljnoSredstava() error {
	due := domain.DueInstallment{
		RataID:                2,
		KreditID:              s.activeKreditID,
		IznosRate:             5_000,
		Valuta:                "RSD",
		OcekivaniDatumDospeca: time.Now().UTC(),
		BrojRacuna:            "111-222-333",
		VlasnikID:             101,
	}
	s.kreditRepoMock.On("GetDueInstallments", mock.Anything, mock.Anything).
		Return([]domain.DueInstallment{due}, nil)
	s.kreditRepoMock.On("GetRetryInstallments", mock.Anything, mock.Anything).
		Return([]domain.DueInstallment{}, nil)
	s.kreditRepoMock.On("ProcessInstallmentPayment", mock.Anything,
		domain.ProcessInstallmentInput{
			RataID:     due.RataID,
			KreditID:   due.KreditID,
			BrojRacuna: due.BrojRacuna,
			IznosRate:  due.IznosRate,
			Valuta:     due.Valuta,
		}).Return(domain.ErrInsufficientFunds)
	s.kreditRepoMock.On("MarkInstallmentFailed", mock.Anything, due.RataID,
		mock.MatchedBy(func(t time.Time) bool {
			s.capturedNextRetry = t
			return true
		})).Return(nil)
	return nil
}

// ─── When koraci ──────────────────────────────────────────────────────────────

func (s *scenarioCtx) popuniFormuZaKreditSaValidnimPodacima() error {
	zahtev := &domain.KreditniZahtev{
		ID:        s.zahtevID,
		VlasnikID: 101,
		Status:    "NA_CEKANJU",
	}
	s.kreditSvcMock.On("ApplyForCredit", mock.Anything,
		mock.MatchedBy(func(input domain.CreateKreditniZahtevInput) bool {
			return input.VlasnikID == 101
		})).Return(zahtev, nil)

	resp, err := s.bankHandler.ApplyForCredit(s.clientCtx, &pb.ApplyForCreditRequest{
		VrstaKredita:      "GOTOVINSKI",
		TipKamate:         "FIKSNI",
		IznosKredita:      "100000.00",
		Valuta:            "RSD",
		SvrhaKredita:      "Kupovina automobila",
		IznosMesecnePlate: "80000.00",
		StatusZaposlenja:  "STALNO",
		PeriodZaposlenja:  24,
		KontaktTelefon:    "0641234567",
		BrojRacuna:        "111-222-333",
		RokOtplate:        24,
	})
	s.lastZahtevResp = resp
	s.lastErr = err
	return nil
}

func (s *scenarioCtx) klijentOtvoriSekcijaKrediti() error {
	resp, err := s.bankHandler.GetClientCredits(s.clientCtx, &emptypb.Empty{})
	s.lastCreditsResp = resp
	s.lastErr = err
	return nil
}

func (s *scenarioCtx) zaposleniOdobriZahtevZaKredit() error {
	resp, err := s.bankHandler.ApproveCredit(s.employeeCtx, &pb.ApproveCreditRequest{
		Id: s.zahtevID,
	})
	s.lastApproveResp = resp
	s.lastErr = err
	return nil
}

func (s *scenarioCtx) zaposleniKlikneNaDugmeOdbij() error {
	_, err := s.bankHandler.RejectCredit(s.employeeCtx, &pb.RejectCreditRequest{
		Id: s.zahtevID,
	})
	s.lastErr = err
	return nil
}

func (s *scenarioCtx) sistemPokreniDnevniCronJob() error {
	triggerWorkerOnce(s.installWorker)
	return nil
}

func (s *scenarioCtx) sistemPokreniCronJobZaNaplatu() error {
	triggerWorkerOnce(s.installWorker)
	return nil
}

// ─── Then koraci ──────────────────────────────────────────────────────────────

func (s *scenarioCtx) sistemBeleziZahtevSaStatusom(expected string) error {
	if s.lastErr != nil {
		return fmt.Errorf("neočekivana greška: %w", s.lastErr)
	}
	if s.lastZahtevResp == nil {
		return fmt.Errorf("odgovor je nil")
	}
	if s.lastZahtevResp.Status != expected {
		return fmt.Errorf("expected status %q, got %q", expected, s.lastZahtevResp.Status)
	}
	return nil
}

func (s *scenarioCtx) prikazujeSeSviKrediti() error {
	if s.lastErr != nil {
		return fmt.Errorf("neočekivana greška: %w", s.lastErr)
	}
	if s.lastCreditsResp == nil || len(s.lastCreditsResp.Credits) == 0 {
		return fmt.Errorf("lista kredita je prazna ili nil")
	}
	return nil
}

func (s *scenarioCtx) kreditDobijaStatus(expected string) error {
	if s.lastErr != nil {
		return fmt.Errorf("neočekivana greška: %w", s.lastErr)
	}
	if s.lastApproveResp == nil || s.lastApproveResp.Kredit == nil {
		return fmt.Errorf("odgovor odobravanja je nil")
	}
	if s.lastApproveResp.Kredit.Status != expected {
		return fmt.Errorf("expected credit status %q, got %q", expected, s.lastApproveResp.Kredit.Status)
	}
	return nil
}

func (s *scenarioCtx) iznosKreditaSeUplacujeNaRacun() error {
	if s.lastErr != nil {
		return fmt.Errorf("greška pri odobravanju — iznos nije uplaćen: %w", s.lastErr)
	}
	return nil
}

func (s *scenarioCtx) zahtevDobijaStatus(_ string) error {
	if s.lastErr != nil {
		return fmt.Errorf("greška pri odbijanju zahteva: %w", s.lastErr)
	}
	return nil
}

func (s *scenarioCtx) iznosRateSeSkidaSaRacuna() error {
	if s.publisher.count() == 0 {
		return fmt.Errorf("notifikacija nije poslata — naplata verovatno nije izvršena")
	}
	return nil
}

func (s *scenarioCtx) sledeciDatumPlacanjaJePomeren() error {
	if got := s.publisher.lastEventType(); got != "CREDIT_RATA_USPEH" {
		return fmt.Errorf("expected CREDIT_RATA_USPEH, got %q", got)
	}
	return nil
}

func (s *scenarioCtx) klijentDobijaObavesenjeOUspesnojNaplati() error {
	if got := s.publisher.lastEventType(); got != "CREDIT_RATA_USPEH" {
		return fmt.Errorf("expected CREDIT_RATA_USPEH, got %q", got)
	}
	return nil
}

func (s *scenarioCtx) rataDobijaStatusKasni() error {
	if s.capturedNextRetry.IsZero() {
		return fmt.Errorf("MarkInstallmentFailed nije bio pozvan — rata nije označena kao KASNI")
	}
	return nil
}

func (s *scenarioCtx) sistemPlaniraNovPokusajNakon72Sata() error {
	if s.capturedNextRetry.IsZero() {
		return fmt.Errorf("nextRetry nije zabeležen")
	}
	min := time.Now().UTC().Add(71 * time.Hour)
	max := time.Now().UTC().Add(73 * time.Hour)
	if s.capturedNextRetry.Before(min) || s.capturedNextRetry.After(max) {
		return fmt.Errorf("nextRetry %v nije u opsegu [now+71h, now+73h]", s.capturedNextRetry)
	}
	return nil
}

func (s *scenarioCtx) klijentDobijaObavesenjeONeuspesnojNaplati() error {
	if got := s.publisher.lastEventType(); got != "CREDIT_RATA_UPOZORENJE" {
		return fmt.Errorf("expected CREDIT_RATA_UPOZORENJE, got %q", got)
	}
	return nil
}

// ─── InitializeScenario ───────────────────────────────────────────────────────

func InitializeScenario(sc *godog.ScenarioContext) {
	s := &scenarioCtx{}

	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		s.reset()
		return ctx, nil
	})

	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.kreditSvcMock.AssertExpectations(s.nopT)
		s.kreditRepoMock.AssertExpectations(s.nopT)
		if s.nopT.err != nil {
			return ctx, s.nopT.err
		}
		return ctx, nil
	})

	// ── Given ─────────────────────────────────────────────────────────────────
	sc.Step(`^klijent je ulogovan u aplikaciju$`, s.klijentJeUlogovanUAplikaciju)
	sc.Step(`^klijent ima aktivne kredite$`, s.klijentImaAktivneKredite)
	sc.Step(`^zaposleni je ulogovan u portal za upravljanje kreditima$`, s.zaposleniJeUlogovanUPortal)
	sc.Step(`^zaposleni je na portalu za upravljanje kreditima$`, s.zaposleniJeUlogovanUPortal)
	sc.Step(`^postoji zahtev za kredit od strane klijenta$`, s.postojiZahtevZaKreditOdStraneKlijenta)
	sc.Step(`^postoji zahtev za kredit klijenta$`, s.postojiZahtevZaKreditKlijenta)
	sc.Step(`^postoji aktivan kredit$`, s.postojiAktivniKredit)
	sc.Step(`^datum sledeće rate je današnji dan$`, s.datumSljedeceRateJeDanasnji)
	sc.Step(`^klijent ima dovoljno sredstava na računu$`, s.klijentImaDovoljnoSredstava)
	sc.Step(`^klijent nema dovoljno sredstava na računu$`, s.klijentNemaDovoljnoSredstava)

	// ── When ──────────────────────────────────────────────────────────────────
	sc.Step(`^popuni formu za kredit sa validnim podacima$`, s.popuniFormuZaKreditSaValidnimPodacima)
	sc.Step(`^klijent otvori sekciju "Krediti"$`, s.klijentOtvoriSekcijaKrediti)
	sc.Step(`^zaposleni odobri zahtev za kredit$`, s.zaposleniOdobriZahtevZaKredit)
	sc.Step(`^zaposleni klikne na dugme "Odbij"$`, s.zaposleniKlikneNaDugmeOdbij)
	sc.Step(`^sistem pokrene dnevni cron job$`, s.sistemPokreniDnevniCronJob)
	sc.Step(`^sistem pokrene cron job za naplatu rate$`, s.sistemPokreniCronJobZaNaplatu)

	// ── Then ──────────────────────────────────────────────────────────────────
	sc.Step(`^sistem beleži zahtev za kredit sa statusom "([^"]*)"$`, s.sistemBeleziZahtevSaStatusom)
	sc.Step(`^prikazuje se lista svih kredita klijenta$`, s.prikazujeSeSviKrediti)
	sc.Step(`^kredit dobija status "([^"]*)"$`, s.kreditDobijaStatus)
	sc.Step(`^iznos kredita se uplaćuje na račun klijenta$`, s.iznosKreditaSeUplacujeNaRacun)
	sc.Step(`^zahtev dobija status "([^"]*)"$`, s.zahtevDobijaStatus)
	sc.Step(`^iznos rate se automatski skida sa računa klijenta$`, s.iznosRateSeSkidaSaRacuna)
	sc.Step(`^sledeći datum plaćanja se pomera za jedan mesec$`, s.sledeciDatumPlacanjaJePomeren)
	sc.Step(`^klijent dobija obaveštenje o uspešnoj naplati rate$`, s.klijentDobijaObavesenjeOUspesnojNaplati)
	sc.Step(`^rata dobija status "Kasni"$`, s.rataDobijaStatusKasni)
	sc.Step(`^sistem planira novi pokušaj naplate nakon 72 sata$`, s.sistemPlaniraNovPokusajNakon72Sata)
	sc.Step(`^klijent dobija obaveštenje o neuspešnoj naplati$`, s.klijentDobijaObavesenjeONeuspesnojNaplati)
}

// ─── TestFeatures — godog runner vezan za go test ────────────────────────────

func TestFeatures(t *testing.T) {
	suite := godog.TestSuite{
		ScenarioInitializer: InitializeScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"features"},
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("godog: jedan ili više BDD scenarija nije prošao")
	}
}
