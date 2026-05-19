# Suivi de livraison des emails (SES) — Spec de conception

Date : 2026-05-19
Statut : approuvé pour passage au plan d'implémentation

## 1. Contexte

L'application event-signup envoie deux emails transactionnels via AWS SES, tous
deux dans le cadre des événements Secret Santa : l'email de **lien magique**
(à l'inscription) et l'email de **révélation** (après le tirage).

Aujourd'hui, `santa_participants.email_sent_at` n'est rempli que lorsque l'appel
`SendEmail` à SES réussit — c'est-à-dire « SES a accepté de prendre l'email en
charge ». Ce n'est **pas** une confirmation de livraison : un email accepté par
SES peut ensuite rebondir (adresse erronée, boîte pleine, domaine inexistant) ou
être marqué comme spam, et ces issues sont aujourd'hui totalement invisibles.

Cette spec ajoute un véritable suivi de livraison : l'application reçoit les
événements SES (envoi, livraison, rebond, plainte, rejet) via SNS et affiche
le statut réel de chaque email dans l'admin.

## 2. Objectifs

- Capter les événements SES `Send`, `Delivery`, `Bounce`, `Complaint`, `Reject`
  pour les deux emails (lien magique et révélation).
- Afficher dans l'admin, par participant, le statut de livraison de chaque email.
- Recevoir les événements via un webhook SNS sécurisé (signature vérifiée).
- Tests écrits avant l'implémentation (TDD).

## 3. Non-objectifs

- Suivi des **ouvertures et clics** (pas de pixel de tracking, pas de
  réécriture de liens) — écarté pour des raisons de vie privée et de pertinence.
- Historique complet événement par événement — on conserve le **statut courant**
  de chaque email, pas un journal d'audit exhaustif.
- Suivi d'emails autres que Secret Santa — l'application n'en envoie pas d'autres.
- Renvoi automatique sur rebond — l'admin voit le rebond et décide.

## 4. Décisions

| Sujet | Choix | Raison |
|---|---|---|
| Événements suivis | Send, Delivery, Bounce, Complaint, Reject | Répond à « qui l'a reçu, qui a rebondi » sans tracking comportemental. |
| Périmètre | Les deux emails (lien magique + révélation) | Un rebond sur le lien magique explique une inscription bloquée ; un rebond sur la révélation = tirage non reçu. |
| Modèle de données | Une ligne par email envoyé, statut mis à jour en place | Suffit pour « voir le statut » ; un historique complet serait du YAGNI. |
| Corrélation | Par **message ID SES** | Robuste face aux renvois multiples à la même adresse. |
| Réception | Webhook SNS, signature vérifiée | Mécanisme standard SES ; l'endpoint est public, la signature est obligatoire. |

## 5. Configuration SES / SNS (côté AWS — appliquée en dernier)

À créer dans le compte AWS propriétaire de `chanteloube.fr` (voir §12) :

- Un **configuration set** SES nommé `event-signup`.
- Une **event destination** sur ce configuration set, de type SNS, publiant les
  types d'événements `SEND`, `DELIVERY`, `BOUNCE`, `COMPLAINT`, `REJECT`.
- Un **topic SNS** `event-signup-ses-events`, avec une *topic policy* autorisant
  le service `ses.amazonaws.com` à y publier.
- Un **abonnement** du topic, protocole HTTPS, vers
  `https://evenements.chanteloube.fr/webhooks/ses`. La confirmation de
  l'abonnement ne peut aboutir qu'une fois le webhook déployé et joignable.

Tout est additif et nommé distinctement : les ressources d'une éventuelle autre
application du compte ne sont pas touchées. La vérification du domaine
`chanteloube.fr` est au niveau du compte — elle est réutilisée, pas refaite.

Les commandes `aws` exactes figureront dans le plan d'implémentation, en
checklist, exécutées en toute dernière étape avec le profil `chanteloube`.

## 6. Modèle de données — nouvelle table `email_messages`

Ajoutée à `schema.sql`. Comme pour `santa_participants`, un `CREATE TABLE IF NOT
EXISTS` couvre les bases neuves et existantes — aucune `migrateColumn` n'est
nécessaire pour une table entièrement nouvelle.

| Colonne | Type | Rôle |
|---|---|---|
| `id` | INTEGER PK | identité |
| `participant_id` | INTEGER NOT NULL FK → `santa_participants(id)` ON DELETE CASCADE | à qui |
| `kind` | TEXT NOT NULL | `link` ou `reveal` |
| `ses_message_id` | TEXT NOT NULL DEFAULT '' | ID renvoyé par SES — clé de corrélation |
| `to_email` | TEXT NOT NULL DEFAULT '' | destinataire (debug) |
| `status` | TEXT NOT NULL DEFAULT 'sent' | `sent` / `delivered` / `bounced` / `complaint` / `rejected` |
| `status_detail` | TEXT NOT NULL DEFAULT '' | sous-type de rebond, diagnostic, type de plainte… |
| `sent_at` | DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP | envoi |
| `updated_at` | DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP | dernier changement de statut |

Contraintes / index : `UNIQUE(participant_id, kind)` (au plus 2 lignes par
participant) ; index sur `ses_message_id` (recherche par le webhook).

**Règle de transition de statut.** Chaque statut a un rang :
`sent`=0, `delivered`=1, `rejected`=2, `bounced`=3, `complaint`=4. Un événement
entrant n'est appliqué que si `rang(nouveau) >= rang(courant)` — ainsi un
`delivered` arrivé en retard n'écrase jamais un `bounced`, mais un `complaint`
postérieur à un `delivered` est bien pris en compte.

## 7. Envoi des emails — capter le message ID

### 7.1 Changement de l'interface `EmailSender`

```
Send(ctx context.Context, to, subject, htmlBody string) (messageID string, err error)
```

(Aujourd'hui : `(…) error`.) Impacts : `SESSender`, `LogSender`,
`fakeEmailSender`, et les appelants `handleSantaRegister`, `sendRevealEmails` /
`sendWithRetry`.

- `SESSender` : nouveau champ `configSet string` ; `SendEmailInput` reçoit
  `ConfigurationSetName` quand `configSet` est non vide ; `Send` renvoie
  `SendEmailOutput.MessageId`.
- `LogSender` et `fakeEmailSender` renvoient un ID synthétique (le
  `fakeEmailSender` renvoie un ID unique par envoi et le mémorise pour les
  assertions de test).

### 7.2 Configuration

Nouvelle variable d'environnement `EVENT_SIGNUP_SES_CONFIGURATION_SET`. Si
définie, `SESSender` l'utilise et les événements remontent. Si vide : pas de
configuration set, pas d'événements — dégradation propre (tous les emails
restent au statut `sent`).

### 7.3 Enregistrement de l'envoi

Après un envoi réussi, `handleSantaRegister` (kind `link`) et `sendRevealEmails`
(kind `reveal`) font un *upsert* de la ligne `email_messages` via
`RecordEmailSent(db, participantID, kind, sesMessageID, toEmail)` :
`UNIQUE(participant_id, kind)` → la ligne est créée, ou réécrite à un renvoi
(nouveau message ID, `status` remis à `sent`). Avec `LogSender` (dev), l'ID est
synthétique, la ligne reste à `sent` faute d'événements — comportement correct.

## 8. Webhook `/webhooks/ses`

- Route `POST /webhooks/ses`, **hors `requireAdmin`** : SNS ne peut pas
  s'authentifier.
- Implémentée dans un nouveau fichier `webhook.go` (concern distinct des handlers
  existants).

### 8.1 Vérification de la signature SNS

`verifySNSMessage(envelope)` :
- Récupère le certificat depuis `SigningCertURL`, après avoir validé que l'URL
  est en HTTPS et que l'hôte se termine par `.amazonaws.com`.
- Reconstruit la chaîne canonique selon les règles SNS (champs différents pour
  `Notification` et `SubscriptionConfirmation`).
- Vérifie la signature : `SHA1withRSA` si `SignatureVersion` vaut `1`,
  `SHA256withRSA` si `2`.
- Les certificats sont mis en cache par URL.
- Échec de vérification → réponse `403`, aucune modification d'état, log.

### 8.2 Traitement des messages

- Type `SubscriptionConfirmation` → l'app appelle le `SubscribeURL` pour
  confirmer l'abonnement ; réponse `200`.
- Type `Notification` → le champ `Message` est l'événement SES (JSON). On lit
  `eventType` et `mail.messageId`. Correspondance des types :
  `Send`→`sent`, `Delivery`→`delivered`, `Bounce`→`bounced`,
  `Complaint`→`complaint`, `Reject`→`rejected`. `status_detail` est rempli depuis
  `bounce.bounceType`/`bounceSubType`, `complaint.complaintFeedbackType`, ou
  `reject.reason` selon le cas.
- `ApplyEmailEvent(db, sesMessageID, status, detail)` met à jour la ligne
  `email_messages` correspondante selon la règle de transition (§6). Message ID
  inconnu (envoi superseded, ou email hors application) → ignoré.
- Le webhook répond toujours `200` à un `Notification` traité ou ignoré, pour
  éviter les ré-essais SNS ; les anomalies sont loggées.

## 9. Affichage admin

Dans `admin_santa.html`, la table des participants : la colonne « Email envoyé »
(booléen) est remplacée par **deux indicateurs de statut** — *lien magique* et
*révélation* — chacun un badge coloré : Envoyé, Remis, Rebondi, Plainte, Rejeté.
La raison d'un rebond (`status_detail`) est visible au survol (`title`).

Le `santaAdminData` charge les lignes `email_messages` de l'événement et les
associe à chaque participant (table indexée par `participant_id` puis `kind`).

Le résumé de la page passe de « X/Y emails envoyés » à un décompte par statut
quand le tirage est fait (ex. « 12 remis · 1 rebond »).

## 10. Cas limites

- SES sans configuration set → emails envoyés, lignes `email_messages` créées au
  statut `sent`, jamais mises à jour. L'admin affiche tout en « Envoyé ».
- `LogSender` (dev) → ID synthétique, ligne au statut `sent`.
- Webhook : message ID inconnu/superseded → ignoré, `200` renvoyé.
- Événements dans le désordre → règle de transition par rang (§6).
- Participant supprimé → ses lignes `email_messages` supprimées en cascade.
- Renvoi d'un email (ré-inscription, ou bouton « Renvoyer ») → ligne `email_messages`
  *upsert* avec le nouveau message ID, statut remis à `sent`.
- Signature SNS invalide → `403`, aucun changement d'état.
- Confirmation d'abonnement SNS → gérée automatiquement.

## 11. Stratégie de tests (écrits avant l'implémentation — TDD)

**Modèle (`email_messages`)** : `RecordEmailSent` crée puis *upsert* (renvoi →
nouveau message ID, statut `sent`) ; `ApplyEmailEvent` met à jour par message ID ;
règle de transition (un `delivered` tardif n'écrase pas un `bounced` ; un
`complaint` écrase un `delivered`) ; message ID inconnu → aucun effet.

**Webhook** : un `Notification` de `Delivery` / `Bounce` / `Complaint` met à jour
la bonne ligne avec le bon statut et le bon détail ; un `SubscriptionConfirmation`
déclenche l'appel de confirmation ; un message ID inconnu est ignoré avec `200`.
La vérification de signature est isolée dans `verifySNSMessage` et contournée en
test via un indicateur sur `App` (comme `AsyncEmail`) ; la construction de la
chaîne canonique a son propre test ciblé.

**Envoi** : `handleSantaRegister` et `sendRevealEmails` créent bien la ligne
`email_messages` avec le message ID renvoyé par le `fakeEmailSender`.

## 12. Prérequis AWS (hors code)

L'AWS CLI de la machine est actuellement connecté au mauvais compte (padmakara,
`117845023176`), qui ne possède pas `chanteloube.fr`. Avant la dernière étape
d'implémentation, ajouter le compte propriétaire de `chanteloube.fr` comme
**profil nommé** `chanteloube` (`aws configure --profile chanteloube`, ou
`aws configure sso` si ce compte utilise IAM Identity Center). Le profil
`default` reste intact ; chaque commande choisit son compte via `--profile`.

L'utilisateur IAM utilisé devra disposer des droits SES (création de
configuration set + event destination) et SNS (création de topic, *topic policy*,
abonnement). À défaut, ces commandes échoueront en `AccessDenied` et la
configuration devra passer par la console.

## 13. Fichiers créés / modifiés

Créés : `webhook.go` (handler `/webhooks/ses` + vérification SNS),
`webhook_test.go`.

Modifiés : `schema.sql` (table `email_messages`), `models.go` (struct
`EmailMessage` + `RecordEmailSent` + `ApplyEmailEvent` + lecture pour l'admin),
`email.go` (signature de `Send`, `SESSender` + configuration set, enregistrement
dans `sendRevealEmails`), `handlers.go` (`handleSantaRegister` enregistre l'envoi,
`santaAdminData` charge les statuts), `main.go` (route `/webhooks/ses`, variable
`EVENT_SIGNUP_SES_CONFIGURATION_SET`), `i18n.go` (libellés de statut),
`templates/admin_santa.html` (colonnes de statut), `testutil_test.go`
(`fakeEmailSender` renvoie un message ID ; contournement de la vérification SNS),
`handlers_test.go` (route webhook dans `newMux`).
