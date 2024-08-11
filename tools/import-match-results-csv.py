import argparse
import csv
import itertools
import json
import os

parser = argparse.ArgumentParser()
parser.add_argument('input_file', help='.csv file to read from')
parser.add_argument('-o', '--output-dir', help='fms_data "matches" subfolder to write json results to', required=True)
parser.add_argument('-v', '--verbose', action='store_true')
parser.add_argument('-y', '--year', type=int, required=True)
parser.add_argument('--skip-existing', action='store_true', help='skip matches already written (default: error)')
args = parser.parse_args()

if os.listdir(args.output_dir) and not args.skip_existing:
    raise RuntimeError('output folder not empty and --skip-existing not set')

REQUIRED_HEADERS = [
    'fms_id', 'comp_level', 'set_number', 'match_number',
    'blue 1', 'blue 2', 'blue 3', 'blue score',
    'red 1', 'red 2', 'red 3', 'red score',
]

def make_match_result():
    return {
        "comp_level": "",
        "match_number": 0,
        "set_number": 0,
        "alliances": {
            "blue": {
                "dqs": [],
                "score": 0,
                "surrogates": [],
                "teams": [],
            },
            "red": {
                "dqs": [],
                "score": 0,
                "surrogates": [],
                "teams": [],
            },
        },
        "score_breakdown": {
            "blue": {},
            "red": {},
        },
    }

def format_match_key(match_result):
    if match_result['comp_level'] == 'qm':
        return f"{match_result['comp_level']}{match_result['match_number']}"
    return f"{match_result['comp_level']}{match_result['set_number']}m{match_result['match_number']}"

def print_verbose(*print_args, **print_kwargs):
    if args.verbose:
        print(*print_args, **print_kwargs)

def normalize_value(key, value):
    try:
        return int(value)
    except ValueError:
        pass
    return value

def normalize_row(row):
    out = {}
    for key in row.keys():
        # lowercase only first segment before '.'
        key_parts = key.split('.')
        key_parts[0] = key_parts[0].lower()
        key_normalized = '.'.join(key_parts)
        if key_normalized in out:
            raise ValueError('Conflicting headers: %r, %r' % (key, key_normalized))
        out[key_normalized] = normalize_value(key_normalized, row[key])
    return out

def iter_alliance_teams():
    for alliance in ('red', 'blue'):
        for team_id in range(1, 4):
            yield (alliance, team_id, '%s %i' % (alliance, team_id))

def team_to_tba_key(team):
    team_key = str(team)
    if not team_key.startswith('frc'):
        team_key = 'frc' + team_key
    return team_key

def assign_teams(row, match_result):
    for alliance, team_id, field in iter_alliance_teams():
        team = str(row[field]).lower()

        if '*' in team:
            team = team.replace('*', '')
            match_result['alliances'][alliance]['surrogates'].append(team_to_tba_key(team))
        if 'd' in team:
            team = team.replace('d', '')
            match_result['alliances'][alliance]['dqs'].append(team_to_tba_key(team))

        match_result['alliances'][alliance]['teams'].append(team_to_tba_key(team))

_warned_unknown_fields = set()
def assign_breakdown(row, match_result, BREAKDOWN_TYPES):
    for key, value in row.items():
        key_parts = key.split('.')
        if key_parts[0] in {'red', 'blue'} and key_parts[1] in BREAKDOWN_TYPES:
            field_type = BREAKDOWN_TYPES[key_parts[1]]
            if field_type is bool:
                field_type = lambda v: bool(int(v))
            try:
                value = field_type(value)
            except ValueError:
                raise ValueError(f'Invalid {BREAKDOWN_TYPES[key_parts[1]]} for field {key!r} in match {format_match_key(match_result)!r}: {value!r}')
            match_result['score_breakdown'][key_parts[0]][key_parts[1]] = value
        elif key not in REQUIRED_HEADERS:
            if key not in _warned_unknown_fields:
                print('warning: skipping unknown field:', key)
                _warned_unknown_fields.add(key)

def validate_breakdown_field(match_result, field, check):
    for alliance in ('red', 'blue'):
        value = match_result['score_breakdown'][alliance][field]
        if not check(value):
            raise ValueError(f'match {format_match_key(match_result)}: {alliance}: invalid {field}: {value}')

class Parser:
    pass

class Parser2022(Parser):
    YEAR = 2022
    # todo: share with go
    DEFAULT_BREAKDOWN_VALUES = {
        "adjustPoints":            0,
        "autoCargoLowerBlue":      0,
        "autoCargoLowerFar":       0,
        "autoCargoLowerNear":      0,
        "autoCargoLowerRed":       0,
        "autoCargoPoints":         0,
        "autoCargoTotal":          0,
        "autoCargoUpperBlue":      0,
        "autoCargoUpperFar":       0,
        "autoCargoUpperNear":      0,
        "autoCargoUpperRed":       0,
        "autoPoints":              0,
        "autoTaxiPoints":          0,
        "cargoBonusRankingPoint":  False,
        "endgamePoints":           0,
        "endgameRobot1":           "None",
        "endgameRobot2":           "None",
        "endgameRobot3":           "None",
        "foulCount":               0,
        "foulPoints":              0,
        "hangarBonusRankingPoint": False,
        "matchCargoTotal":         0,
        "quintetAchieved":         False,
        "rp":                      0,
        "taxiRobot1":              "No",
        "taxiRobot2":              "No",
        "taxiRobot3":              "No",
        "techFoulCount":           0,
        "teleopCargoLowerBlue":    0,
        "teleopCargoLowerFar":     0,
        "teleopCargoLowerNear":    0,
        "teleopCargoLowerRed":     0,
        "teleopCargoPoints":       0,
        "teleopCargoTotal":        0,
        "teleopCargoUpperBlue":    0,
        "teleopCargoUpperFar":     0,
        "teleopCargoUpperNear":    0,
        "teleopCargoUpperRed":     0,
        "teleopPoints":            0,
        "totalPoints":             0,
    }
    BREAKDOWN_TYPES = {k: type(v) for k, v in DEFAULT_BREAKDOWN_VALUES.items()}

    VALID_HANGAR_RESULTS = set(map(sum, itertools.product((0, 4, 6, 10, 15), repeat=3)))
    RP_REQUIRED_FIELDS = ('rp', 'cargoBonusRankingPoint', 'hangarBonusRankingPoint', 'totalPoints')

    @classmethod
    def validate_match_result(cls, match_result):
        for alliance, other_alliance in itertools.permutations(('red', 'blue'), 2):
            breakdown = match_result['score_breakdown'][alliance]
            if all(field in breakdown for field in cls.RP_REQUIRED_FIELDS):
                expected_rp = 0
                score_diff = breakdown['totalPoints'] - match_result['score_breakdown'][other_alliance]['totalPoints']
                if score_diff > 0:
                    expected_rp += 2
                elif score_diff == 0:
                    expected_rp += 1
                if breakdown['cargoBonusRankingPoint']:
                    expected_rp += 1
                if breakdown['hangarBonusRankingPoint']:
                    expected_rp += 1
                if match_result['comp_level'] != 'qm':
                    expected_rp = 0  # match FMS
                if breakdown['rp'] != expected_rp:
                    raise ValueError('%s: expected rp = %r, got rp = %r' % (alliance, expected_rp, breakdown['rp']))

            if 'endgamePoints' in breakdown:
                if breakdown['endgamePoints'] not in cls.VALID_HANGAR_RESULTS:
                    raise ValueError('%s: invalid endgamePoints: %r' % (alliance, breakdown['endgamePoints']))


class Parser2024(Parser):
    YEAR = 2024
    # todo: share with go
    DEFAULT_BREAKDOWN_VALUES = {
        "adjustPoints": 0,
        "autoAmpNoteCount": 0,
        "autoAmpNotePoints": 0,
        "autoLeavePoints": 0,
        "autoLineRobot1": "No",
        "autoLineRobot2": "No",
        "autoLineRobot3": "No",
        "autoPoints": 0,
        "autoSpeakerNoteCount": 0,
        "autoSpeakerNotePoints": 0,
        "autoTotalNotePoints": 0,
        "coopNotePlayed": False,
        "coopertitionBonusAchieved": False,
        "coopertitionCriteriaMet": False,
        "endGameHarmonyPoints": 0,
        "endGameNoteInTrapPoints": 0,
        "endGameOnStagePoints": 0,
        "endGameParkPoints": 0,
        "endGameRobot1": "None",
        "endGameRobot2": "None",
        "endGameRobot3": "None",
        "endGameSpotLightBonusPoints": 0,
        "endGameTotalStagePoints": 0,
        "ensembleBonusAchieved": False,
        "ensembleBonusOnStageRobotsThreshold": 0,
        "ensembleBonusStagePointsThreshold": 0,
        "foulCount": 0,
        "foulPoints": 0,
        "g206Penalty": False,
        "g408Penalty": False,
        "g424Penalty": False,
        "melodyBonusAchieved": False,
        "melodyBonusThreshold": 0,
        "melodyBonusThresholdCoop": 0,
        "melodyBonusThresholdNonCoop": 0,
        "micCenterStage": False,
        "micStageLeft": False,
        "micStageRight": False,
        "rp": 0,
        "techFoulCount": 0,
        "teleopAmpNoteCount": 0,
        "teleopAmpNotePoints": 0,
        "teleopPoints": 0,
        "teleopSpeakerNoteAmplifiedCount": 0,
        "teleopSpeakerNoteAmplifiedPoints": 0,
        "teleopSpeakerNoteCount": 0,
        "teleopSpeakerNotePoints": 0,
        "teleopTotalNotePoints": 0,
        "totalPoints": 0,
        "trapCenterStage": False,
        "trapStageLeft": False,
        "trapStageRight": False
    }
    BREAKDOWN_TYPES = {k: type(v) for k, v in DEFAULT_BREAKDOWN_VALUES.items()}

    VALID_STAGE_RESULTS = set(map(sum, itertools.product((0, 4, 6, 10, 15), repeat=3)))

    @classmethod
    def validate_match_result(cls, match_result):
        # TODO: check RP like 2022

        validate_breakdown_field(match_result, 'foulPoints',
            lambda value: isinstance(value, int) and (value in (0, 2, 4) or value >= 5))
        validate_breakdown_field(match_result, 'endGameTotalStagePoints',
            lambda value: 0 <= value <= 31)

parsers = {cls.YEAR: cls for cls in Parser.__subclasses__()}
parser = parsers[args.year]()

all_match_results = {}

with open(args.input_file, newline='') as input_file:
    reader = csv.DictReader(input_file)
    for i, row in enumerate(reader):
        row_number = i + 2
        row = normalize_row(row)

        missing_headers = [h for h in REQUIRED_HEADERS if h not in row]
        if missing_headers:
            raise ValueError('row %i missing headers: %r' % (row_number, missing_headers))

        missing_values = [h for h in REQUIRED_HEADERS if row[h] == '']
        if missing_values:
            print_verbose('skipping row %i: missing %r' % (row_number, missing_values))
            continue

        fms_id = row['fms_id']
        if fms_id in all_match_results:
            raise ValueError('duplicate fms_id: %r' % fms_id)

        match_result = make_match_result()

        for field in ('comp_level', 'set_number', 'match_number'):
            match_result[field] = row[field]

        for alliance in ('red', 'blue'):
            score = row['%s score' % alliance]
            match_result['alliances'][alliance]['score'] = score
            match_result['score_breakdown'][alliance]['totalPoints'] = score

        try:
            assign_teams(row, match_result)
            assign_breakdown(row, match_result, BREAKDOWN_TYPES=parser.BREAKDOWN_TYPES)

            parser.validate_match_result(match_result)
        except ValueError as e:
            raise ValueError('In row %i: %s' % (row_number, e))

        all_match_results[fms_id] = match_result

count = 0
for fms_id, match_result in all_match_results.items():
    html_path = os.path.join(args.output_dir, '%s.html' % fms_id)
    if os.path.exists(html_path):
        print_verbose('skipping match %s: %r exists' % (fms_id, html_path))
        continue

    json_path = os.path.join(args.output_dir, '%s.json' % fms_id)
    if os.path.exists(json_path):
        print_verbose('skipping match %s: %r exists' % (fms_id, json_path))
        continue

    with open(html_path, 'w') as f:
        pass  # leave html file empty, only needed for listing

    with open(json_path, 'w') as f:
        json.dump(match_result, f, indent=2)
        print_verbose('wrote %r' % json_path)

    count += 1

print(f'Wrote {count} matches')
